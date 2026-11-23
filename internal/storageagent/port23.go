package storageagent

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// The programs port 23 runs, as seen inside an account's chroot, whose
// /home is the account's own data (E17).
const (
	chrootHome = "/home"
	sftpServer = "/usr/lib/openssh/sftp-server"
	scpBin     = "/usr/bin/scp"
	rrsyncBin  = "/usr/bin/rrsync"
	borgBin    = "/usr/bin/borg" // borg 1.4 serves every requested version
)

// errNotAllowed is the answer to every refused command, the message
// Hetzner shows the client.
var errNotAllowed = errors.New("Command not allowed") //nolint:staticcheck // the message Hetzner shows the client

// borgNames are the program names a borg client may ask for, plain or
// through --remote-path.
var borgNames = []string{"borg", "borg-1.1", "borg-1.2", "borg-1.4"}

// borgOption matches the options a borg client passes to borg serve: the
// log level, JSON logging, the umask and debug topics. A client's own
// --restrict-to-path is not among them.
var borgOption = regexp.MustCompile(`^(--debug|--info|--warning|--error|--critical|--log-json|--umask=0?[0-7]{3}|--debug-topic=[A-Za-z0-9_.]+)$`)

// Port23 maps the SSH_ORIGINAL_COMMAND of a port 23 login of account user
// to the argv to exec, never through a shell: sftp-server, scp -t or -f
// with one path inside the home, rsync --server through rrsync and borg
// serve, all restricted to the home. Read-only accounts get sftp-server
// -R, rrsync -ro and no scp -t. Everything else, including any shell
// metacharacter, and any user that is no storage account, is refused with
// "Command not allowed".
func Port23(user string, readOnly bool, command string) (argv []string, err error) {
	if !boxUserRE.MatchString(user) && !subUserRE.MatchString(user) {
		return nil, errNotAllowed
	}
	args, ok := words(command)
	if !ok || len(args) == 0 {
		return nil, errNotAllowed
	}
	switch name := args[0]; {
	case len(args) == 1 && (name == "internal-sftp" || name == "sftp-server" || name == sftpServer):
		argv = []string{sftpServer, "-d", chrootHome}
		if readOnly {
			argv = append(argv, "-R")
		}
		return argv, nil
	case name == "scp":
		return scpArgv(args[1:], readOnly)
	case name == "rsync" && len(args) > 1 && args[1] == "--server":
		// rrsync checks the rsync arguments itself; it reads them from
		// SSH_ORIGINAL_COMMAND.
		if readOnly {
			return []string{rrsyncBin, "-ro", chrootHome}, nil
		}
		return []string{rrsyncBin, chrootHome}, nil
	case slices.Contains(borgNames, name) && len(args) > 1 && args[1] == "serve":
		argv = []string{borgBin, "serve", "--restrict-to-path", chrootHome}
		for _, a := range args[2:] {
			if !borgOption.MatchString(a) {
				return nil, errNotAllowed
			}
			argv = append(argv, a)
		}
		return argv, nil
	}
	return nil, errNotAllowed
}

// words splits command at spaces. It refuses a command holding anything
// but letters, digits, a few punctuation characters that no shell treats
// specially, spaces and non-ASCII bytes.
func words(command string) ([]string, bool) {
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c >= 0x80 || c == ' ' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
			strings.IndexByte("-_./=+,:@%^", c) >= 0 {
			continue
		}
		return nil, false
	}
	return strings.Fields(command), true
}

// scpArgv parses the arguments OpenSSH's scp sends for the remote side,
// "[-v] [-r] [-p] [-d] -t|-f [--] <path>", and returns the scp argv.
func scpArgv(args []string, readOnly bool) ([]string, error) {
	var flags []string
	mode := ""
	i := 0
loop:
	for ; i < len(args); i++ {
		switch a := args[i]; a {
		case "-v", "-r", "-p", "-d":
			flags = append(flags, a)
		case "-t", "-f":
			if mode != "" {
				return nil, errNotAllowed
			}
			mode = a
		case "--":
			i++
			break loop
		default:
			if strings.HasPrefix(a, "-") {
				return nil, errNotAllowed
			}
			break loop
		}
	}
	if mode == "" || (readOnly && mode == "-t") || len(args)-i != 1 || !inHome(args[i]) {
		return nil, errNotAllowed
	}
	return append(append([]string{scpBin}, flags...), mode, "--", args[i]), nil
}

// inHome reports whether p stays in the home: relative or below /home,
// and without a ".." element.
func inHome(p string) bool {
	if p == "" || slices.Contains(strings.Split(p, "/"), "..") {
		return false
	}
	return !strings.HasPrefix(p, "/") || p == chrootHome || strings.HasPrefix(p, chrootHome+"/")
}
