package storageagent

import "io"

// AuthorizedKeys returns the OpenSSH key lines of r, which may hold OpenSSH
// lines and RFC4716 blocks. Until plan 19-07 builds it, it returns none.
func AuthorizedKeys(r io.Reader) []string {
	return nil
}
