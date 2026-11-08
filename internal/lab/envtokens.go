package lab

import (
	"path/filepath"

	"github.com/webcodingcat/hlab/internal/state"
)

const (
	// EnvTokenFile is the file in the data folder that keeps each project's
	// env token in clear text, readable like the control token. The state
	// keeps tokens only as hashes, and `hlab env` must print the same token
	// every time.
	EnvTokenFile = "env-tokens.json"
	// EnvTokenName is the name of the env token in its project.
	EnvTokenName = "hlab-env"
)

// EnvToken returns the project's env token: a read_write token that stays
// the same across calls, shells and restarts, for `hlab env`. It makes a new
// one when the project has none yet, or when its token was deleted.
func (l *Lab) EnvToken(projectID int64) (string, error) {
	l.envMu.Lock()
	defer l.envMu.Unlock()
	path := filepath.Join(l.dataDir, EnvTokenFile)
	secrets, err := l.system.ReadSecrets(path)
	if err != nil {
		return "", err
	}
	if secret, found := secrets[projectID]; found && l.isEnvToken(projectID, secret) {
		return secret, nil
	}
	// the state first, the file after: if the file cannot be written, the
	// token in the state is not in the file, and the next call makes a new one
	secret := newSecret()
	var gone []int64
	err = l.Change(func(tx *state.Tx) error {
		if _, err := state.Get(tx, state.Projects, projectID); err != nil {
			return err
		}
		if old, found := state.ByName(tx, state.Tokens, projectID, EnvTokenName); found {
			state.Delete(tx, state.Tokens, old.ID)
		}
		l.putToken(tx, projectID, EnvTokenName, state.PermissionReadWrite, secret)
		for kept := range secrets {
			if _, err := state.Get(tx, state.Projects, kept); err != nil {
				gone = append(gone, kept)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	// the file keeps only projects that are still there
	for _, projectGone := range gone {
		delete(secrets, projectGone)
	}
	secrets[projectID] = secret
	if err := l.system.WriteSecrets(path, secrets); err != nil {
		return "", err
	}
	return secret, nil
}

// isEnvToken tells whether a secret is still the project's env token.
func (l *Lab) isEnvToken(projectID int64, secret string) bool {
	token, found := l.TokenByHash(state.HashToken(secret))
	return found && token.ProjectID == projectID && token.Name == EnvTokenName
}
