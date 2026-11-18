//go:build windows

package windows

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	xwin "golang.org/x/sys/windows"
)

// noConsoleSession is what WTSGetActiveConsoleSessionId returns when no
// session is attached to the console.
const noConsoleSession = 0xFFFFFFFF

// consoleUserWSLConfig reads %UserProfile%\.wslconfig of the user logged on
// at the console. found is false when there is no file or no console user;
// a token that cannot be obtained (nobody logged on, or the caller lacks
// the rights of LocalSystem) counts as no console user.
func consoleUserWSLConfig(context.Context) ([]byte, bool, error) {
	session := xwin.WTSGetActiveConsoleSessionId()
	if session == noConsoleSession {
		return nil, false, nil
	}
	var token xwin.Token
	if err := xwin.WTSQueryUserToken(session, &token); err != nil {
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
