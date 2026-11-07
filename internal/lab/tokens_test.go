package lab_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
)

func TestCreateTokenKeepsOnlyItsHash(t *testing.T) {
	dataDir := t.TempDir()
	l, _ := open(t, lab.Options{DataDir: dataDir})
	project, _ := l.CreateProject("default", state.Limits{})

	token, secret, err := l.CreateToken(project.ID, "ci", state.PermissionRead)
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 64 || strings.Trim(secret, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789") != "" {
		t.Errorf("the token %q is not 64 letters and digits", secret)
	}
	if token.Hash != state.HashToken(secret) || token.Prefix != secret[:4] || token.Permission != state.PermissionRead {
		t.Errorf("the record is %+v", token)
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(dataDir, lab.StateFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), secret) {
		t.Error("the state file holds the token in clear text")
	}
}

func TestCreateTokenChecksItsInput(t *testing.T) {
	l, _ := open(t, lab.Options{})
	project, _ := l.CreateProject("default", state.Limits{})
	if _, _, err := l.CreateToken(project.ID, "ci", state.PermissionReadWrite); err != nil {
		t.Fatal(err)
	}

	for name, err := range map[string]error{
		"a second ci":        errOf2(l.CreateToken(project.ID, "ci", state.PermissionRead)),
		"no name":            errOf2(l.CreateToken(project.ID, "", state.PermissionRead)),
		"a long name":        errOf2(l.CreateToken(project.ID, strings.Repeat("a", 65), state.PermissionRead)),
		"another permission": errOf2(l.CreateToken(project.ID, "admin", "admin")),
		"no project":         errOf2(l.CreateToken(project.ID+1, "ci", state.PermissionRead)),
	} {
		want := map[string]error{"a second ci": lab.ErrExists, "no project": state.ErrNotFound}[name]
		if want == nil {
			want = lab.ErrInvalid
		}
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
	}
}

func TestTokensAreFoundByHashUntilDeleted(t *testing.T) {
	l, _ := open(t, lab.Options{})
	project, _ := l.CreateProject("default", state.Limits{})
	other, _ := l.CreateProject("other", state.Limits{})
	token, secret, _ := l.CreateToken(project.ID, "ci", state.PermissionReadWrite)
	_, _, _ = l.CreateToken(other.ID, "ci", state.PermissionRead)

	if found, ok := l.TokenByHash(state.HashToken(secret)); !ok || found.ID != token.ID {
		t.Errorf("the token is %+v, %v", found, ok)
	}
	if tokens, err := l.Tokens(project.ID); err != nil || len(tokens) != 1 || tokens[0].ID != token.ID {
		t.Errorf("the project's tokens are %+v, %v", tokens, err)
	}

	if err := l.DeleteToken(token.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.TokenByHash(state.HashToken(secret)); ok {
		t.Error("a deleted token is still found")
	}
	if err := l.DeleteToken(token.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("deleting it again: %v, want ErrNotFound", err)
	}
	if _, err := l.Tokens(other.ID + 1); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("tokens of no project: %v, want ErrNotFound", err)
	}
}

// errOf2 returns the error of a call that returns two values too.
func errOf2[A, B any](_ A, _ B, err error) error {
	return err
}
