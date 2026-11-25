package storageagent

import (
	"bytes"
	"context"
	"errors"
	"io"
)

// runIO executes a command like run, with stdin and its standard output
// and standard error apart. On failure the error carries only the
// standard error, so a standard output that may hold a secret stays with
// the caller.
func runIO(ctx context.Context, stdin io.Reader, name string, args ...string) (stdout, stderr string, err error) {
	var o, e bytes.Buffer
	err = runBounded(ctx, stdin, &o, &e, e.Bytes, name, args...)
	if errors.Is(err, errNotExited) {
		return "", "", err
	}
	return o.String(), e.String(), err
}
