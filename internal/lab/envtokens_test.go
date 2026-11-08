package lab_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
)

// envToken returns a project's env token, failing the test on an error.
func envToken(t *testing.T, l *lab.Lab, projectID int64) string {
	t.Helper()
	secret, err := l.EnvToken(projectID)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestEnvTokenStaysTheSameAcrossCallsAndRestarts(t *testing.T) {
	dataDir := t.TempDir()
	machine := fake.New()
	l := start(t, machine, lab.Options{DataDir: dataDir})
	project, err := l.CreateProject("web", state.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	first := envToken(t, l, project.ID)
	if token, found := l.TokenByHash(state.HashToken(first)); !found || token.Name != lab.EnvTokenName || token.Permission != state.PermissionReadWrite {
		t.Errorf("got %+v, %v, want a read_write token named %s", token, found, lab.EnvTokenName)
	}
	if again := envToken(t, l, project.ID); again != first {
		t.Errorf("the second call got another token")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := start(t, machine, lab.Options{DataDir: dataDir})
	defer func() { _ = restarted.Close() }()
	if again := envToken(t, restarted, project.ID); again != first {
		t.Errorf("after a restart the lab gave another token")
	}
}

func TestADeletedEnvTokenIsMadeAnew(t *testing.T) {
	l, _ := open(t, lab.Options{})
	project, _ := l.CreateProject("web", state.Limits{})
	first := envToken(t, l, project.ID)
	token, _ := l.TokenByHash(state.HashToken(first))
	if err := l.DeleteToken(token.ID); err != nil {
		t.Fatal(err)
	}
	second := envToken(t, l, project.ID)
	if second == first {
		t.Error("the deleted token came back")
	}
	tokens, _ := l.Tokens(project.ID)
	if len(tokens) != 1 {
		t.Errorf("got %d tokens, want one", len(tokens))
	}
}

func TestTheEnvTokenFileKeepsOnlyProjectsThatAreThere(t *testing.T) {
	dataDir := t.TempDir()
	l, _ := open(t, lab.Options{DataDir: dataDir})
	gone, _ := l.CreateProject("gone", state.Limits{})
	kept, _ := l.CreateProject("kept", state.Limits{})
	envToken(t, l, gone.ID)
	if err := l.DeleteProject(gone.ID); err != nil {
		t.Fatal(err)
	}
	secret := envToken(t, l, kept.ID)

	data, err := os.ReadFile(filepath.Join(dataDir, lab.EnvTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	var secrets map[int64]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 1 || secrets[kept.ID] != secret {
		t.Errorf("the file holds %v, want only the project kept", secrets)
	}
	if _, err := l.EnvToken(gone.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a deleted project: got %v, want state.ErrNotFound", err)
	}
}

// An env token file of null, as an editor might leave it, counts as no
// tokens: the project gets a new one.
func TestEnvTokenTakesAFileOfNullAsNoTokens(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, lab.EnvTokenFile), []byte("null\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := start(t, fake.New(), lab.Options{DataDir: dataDir})
	project, err := l.CreateProject("web", state.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	secret := envToken(t, l, project.ID)
	if token, found := l.TokenByHash(state.HashToken(secret)); !found || token.ProjectID != project.ID || token.Name != lab.EnvTokenName {
		t.Errorf("got %+v, %v, want the project's env token", token, found)
	}
}
