package storageagent

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// chrootBase holds one root-owned chroot per account (E17); sshd chroots
// into chrootBase/<user>, whose home is a bind mount of the account's data.
const chrootBase = "/srv/chroot"

// maxKeysBytes limits how much of an authorized_keys file is read.
const maxKeysBytes = 1 << 20

// keyTypeRE matches the name of an SSH public key algorithm.
var keyTypeRE = regexp.MustCompile(`^[a-z0-9][a-z0-9@.-]*$`)

// AccountKeys returns the keys of account user from .ssh/authorized_keys
// in the home of its chroot, the account's own data; symlinks cannot lead
// out of that home. A missing file or home yields no keys. It is the
// AuthorizedKeysCommand of sshd (hlab-agent storage-keys).
func AccountKeys(user string) ([]string, error) {
	if !boxUserRE.MatchString(user) && !subUserRE.MatchString(user) {
		return nil, fmt.Errorf("invalid account %q", user)
	}
	r, err := os.OpenRoot(filepath.Join(chrootBase, user, "home"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	name := filepath.Join(".ssh", "authorized_keys")
	st, err := r.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s of %s is no regular file", name, user)
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return AuthorizedKeys(io.LimitReader(f, maxKeysBytes)), nil
}

// AuthorizedKeys returns the keys of r as OpenSSH lines "<type> <base64>".
// r may hold OpenSSH lines and RFC4716 blocks; lines with key options,
// comments and anything that is no well-formed key are left out.
func AuthorizedKeys(r io.Reader) []string {
	var keys []string
	sc := bufio.NewScanner(r)
	var block []string // body lines of an open RFC4716 block
	inBlock, header := false, false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "---- BEGIN SSH2 PUBLIC KEY ----":
			inBlock, header, block = true, false, nil
		case inBlock && line == "---- END SSH2 PUBLIC KEY ----":
			if k, ok := keyLine("", strings.Join(block, "")); ok {
				keys = append(keys, k)
			}
			inBlock = false
		case inBlock && header:
			// A header continues while its lines end in a backslash.
			header = strings.HasSuffix(line, `\`)
		case inBlock && strings.Contains(line, ":"):
			header = strings.HasSuffix(line, `\`)
		case inBlock:
			block = append(block, line)
		default:
			if f := strings.Fields(line); len(f) >= 2 {
				if k, ok := keyLine(f[0], f[1]); ok {
					keys = append(keys, k)
				}
			}
		}
	}
	return keys
}

// keyLine decodes the base64 key blob b64 and returns its OpenSSH line;
// the algorithm named inside the blob must be a key type and equal typ
// unless typ is empty.
func keyLine(typ, b64 string) (string, bool) {
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(blob) < 4 {
		return "", false
	}
	n := binary.BigEndian.Uint32(blob)
	if uint64(n) > uint64(len(blob)-4) {
		return "", false
	}
	name := string(blob[4 : 4+n])
	if !keyTypeRE.MatchString(name) || (typ != "" && typ != name) || len(blob) == 4+int(n) {
		return "", false
	}
	return name + " " + base64.StdEncoding.EncodeToString(blob), true
}
