package agent

import "errors"

// dumpAttempts is how often a netlink dump interrupted by a concurrent
// change of the kernel tables is made before its error is returned.
const dumpAttempts = 5

// retryDump calls list again while it fails with interrupted, at most
// dumpAttempts times in all; an interrupted dump may be incomplete.
func retryDump[T any](interrupted error, list func() (T, error)) (T, error) {
	var (
		v   T
		err error
	)
	for range dumpAttempts {
		v, err = list()
		if !errors.Is(err, interrupted) {
			break
		}
	}
	return v, err
}
