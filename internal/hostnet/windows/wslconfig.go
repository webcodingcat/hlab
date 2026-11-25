//go:build windows

package windows

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// consoleUserWSLConfig reads %UserProfile%\.wslconfig of the interactive
// user (see interactiveSession). found is false when there is no file or no
// interactive user; a token that cannot be obtained (nobody logged on, or
// the caller lacks the rights of LocalSystem) counts as no interactive user.
func consoleUserWSLConfig(context.Context) ([]byte, bool, error) {
	token, err := interactiveUserToken()
	if err != nil {
		return nil, false, nil
	}
	defer func() { _ = token.Close() }()
	dir, err := token.GetUserProfileDirectory()
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, ".wslconfig"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}
