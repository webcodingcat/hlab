package lab

import (
	"crypto/rand"
	"fmt"

	"github.com/webcodingcat/hlab/internal/state"
)

const (
	// tokenLength is the length of an API token, as Hetzner's are long.
	tokenLength = 64
	// tokenAlphabet is what Hetzner's tokens are made of.
	tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	// tokenPrefixLength is how much of a token the lab keeps to tell tokens
	// apart in lists.
	tokenPrefixLength = 4
	// maxTokenName is the longest token name.
	maxTokenName = 64
)

// CreateToken adds an API token to a project and returns it with the token
// itself, which the lab shows only now: the state keeps only its hash. Token
// names are unique in their project, so that the CLI can name a token.
func (l *Lab) CreateToken(projectID int64, name string, permission state.Permission) (state.Token, string, error) {
	if name == "" || len(name) > maxTokenName {
		return state.Token{}, "", fmt.Errorf("token name %q: %w: use 1 to %d characters", name, ErrInvalid, maxTokenName)
	}
	if permission != state.PermissionRead && permission != state.PermissionReadWrite {
		return state.Token{}, "", fmt.Errorf("permission %q: %w: use %s or %s", permission, ErrInvalid, state.PermissionRead, state.PermissionReadWrite)
	}
	secret := newSecret()
	var token state.Token
	err := l.Change(func(tx *state.Tx) error {
		if _, err := state.Get(tx, state.Projects, projectID); err != nil {
			return err
		}
		if _, found := tokenByName(tx, projectID, name); found {
			return fmt.Errorf("token %q: %w", name, ErrExists)
		}
		token = l.putToken(tx, projectID, name, permission, secret)
		return nil
	})
	if err != nil {
		return state.Token{}, "", err
	}
	return token, secret, nil
}

// putToken adds the record of a token to the state and returns it.
func (l *Lab) putToken(tx *state.Tx, projectID int64, name string, permission state.Permission, secret string) state.Token {
	token := state.Token{
		ID:         state.NewID(tx, state.Tokens),
		ProjectID:  projectID,
		Name:       name,
		Created:    l.Now(),
		Permission: permission,
		Hash:       state.HashToken(secret),
		Prefix:     secret[:tokenPrefixLength],
	}
	state.Put(tx, state.Tokens, &token)
	return token
}

// tokenByName returns a project's token of that name, if there is one.
func tokenByName(tx *state.Tx, projectID int64, name string) (*state.Token, bool) {
	for _, token := range state.All(tx, state.Tokens) {
		if token.ProjectID == projectID && token.Name == name {
			return token, true
		}
	}
	return nil, false
}

// newSecret returns a new random token.
func newSecret() string {
	secret := make([]byte, 0, tokenLength)
	random := make([]byte, tokenLength)
	for len(secret) < tokenLength {
		_, _ = rand.Read(random) // never fails, as its documentation says
		for _, b := range random {
			// bytes beyond the last whole multiple of the alphabet would make
			// its first letters more likely
			if int(b) < 256/len(tokenAlphabet)*len(tokenAlphabet) && len(secret) < tokenLength {
				secret = append(secret, tokenAlphabet[int(b)%len(tokenAlphabet)])
			}
		}
	}
	return string(secret)
}

// Tokens returns a project's tokens, by ID, or state.ErrNotFound for a
// project that is not there.
func (l *Lab) Tokens(projectID int64) ([]state.Token, error) {
	var tokens []state.Token
	err := l.View(func(tx *state.Tx) error {
		if _, err := state.Get(tx, state.Projects, projectID); err != nil {
			return err
		}
		for _, token := range state.All(tx, state.Tokens) {
			if token.ProjectID == projectID {
				tokens = append(tokens, *token)
			}
		}
		return nil
	})
	return tokens, err
}

// DeleteToken removes a token; requests with it fail from now on.
func (l *Lab) DeleteToken(tokenID int64) error {
	return l.Change(func(tx *state.Tx) error {
		if _, err := state.Get(tx, state.Tokens, tokenID); err != nil {
			return err
		}
		state.Delete(tx, state.Tokens, tokenID)
		return nil
	})
}

// TokenByHash returns the token with that hash (state.HashToken), if there is
// one: the API's token check asks it on every request.
func (l *Lab) TokenByHash(hash string) (state.Token, bool) {
	var token state.Token
	var found bool
	_ = l.View(func(tx *state.Tx) error {
		// hashes are unique: a token is 64 random characters
		match, ok := state.Find(tx, state.Tokens, func(candidate *state.Token) bool { return candidate.Hash == hash })
		if ok {
			token, found = *match, true
		}
		return nil
	})
	return token, found
}
