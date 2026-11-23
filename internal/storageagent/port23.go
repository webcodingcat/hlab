package storageagent

import "errors"

// Port23 maps the SSH_ORIGINAL_COMMAND of a port 23 login to the argv to
// exec. Until plan 19-07 builds the command set it refuses everything.
func Port23(user string, readOnly bool, command string) (argv []string, err error) {
	return nil, errors.New("Command not allowed") //nolint:staticcheck // the message Hetzner shows the client
}
