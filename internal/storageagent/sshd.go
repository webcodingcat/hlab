//go:build linux

package storageagent

import (
	"fmt"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// sshdConfigFile is the one sshd configuration of the storage VM, written
// whole by the agent; agentBinary is where the storage VM installs
// hlab-agent, inside /usr so that the chroots see it.
const (
	sshdConfigFile = "/etc/ssh/sshd_config"
	agentBinary    = "/usr/local/sbin/hlab-agent"
)

// sshdConfig returns the sshd configuration for doc, leaving out the
// accounts in skipped and the subaccounts of a skipped box.
//
// Port 22 runs internal-sftp in the account's chroot, read-only unless the
// account may write; port 23 runs hlab-agent storage-shell (Port23) for
// accounts with SSH enabled and refuses the others. Keys come from
// hlab-agent storage-keys on both ports. Accounts that are not reachable
// externally are refused for sources outside the public range or inside
// the host's transit network. Only the accounts of doc may log in.
//
// sshd applies the first value of a keyword among the matching Match
// blocks over the global one, so the specific blocks come first.
func sshdConfig(doc agentapi.Storage, skipped map[string]bool) string {
	var all, writable, ssh, sshReadOnly, internal []string
	var allowLines []string
	add := func(user string, a agentapi.Access) {
		all = append(all, user)
		if !a.ReadOnly {
			writable = append(writable, user)
		}
		if a.SSH {
			ssh = append(ssh, user)
			if a.ReadOnly {
				sshReadOnly = append(sshReadOnly, user)
			}
		}
		if !a.ReachableExternally {
			internal = append(internal, user)
		}
	}
	for _, b := range doc.Boxes {
		if skipped[b.User] {
			continue
		}
		users := []string{b.User}
		add(b.User, agentapi.Access{SSH: b.Access.SSH, ReachableExternally: b.Access.ReachableExternally})
		for _, s := range b.Subaccounts {
			if !skipped[s.User] {
				users = append(users, s.User)
				add(s.User, s.Access)
			}
		}
		allowLines = append(allowLines, "AllowUsers "+strings.Join(users, " "))
	}

	var c strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&c, format+"\n", args...) }
	line("# Written by hlab-agent (role storage); every apply replaces it.")
	line("Port 22")
	line("Port 23")
	line("PermitRootLogin no")
	line("PasswordAuthentication yes")
	line("KbdInteractiveAuthentication no")
	line("PubkeyAuthentication yes")
	line("UsePAM yes")
	line("AuthorizedKeysFile none")
	line("AuthorizedKeysCommand %s storage-keys %%u", agentBinary)
	line("AuthorizedKeysCommandUser root")
	line("DisableForwarding yes")
	line("PermitTTY no")
	line("PermitUserRC no")
	line("PermitTunnel no")
	line("PrintMotd no")
	line("ChrootDirectory %s/%%u", chrootBase)
	line("Subsystem sftp internal-sftp")
	line("ForceCommand internal-sftp -R -d %s", chrootHome)
	if len(allowLines) == 0 {
		line("DenyUsers *")
	}
	for _, l := range allowLines {
		line("%s", l)
	}

	if len(internal) > 0 {
		users := strings.Join(internal, ",")
		line("Match User %s Address %s", users, doc.HostNet.Masked())
		line("\tDenyUsers *")
		if doc.PublicRange.IsValid() {
			line("Match User %s Address *,!%s", users, doc.PublicRange.Masked())
			line("\tDenyUsers *")
		}
	}
	if len(ssh) == 0 {
		line("Match LocalPort 23")
	} else {
		line("Match LocalPort 23 User *,!%s", strings.Join(ssh, ",!"))
	}
	line("\tDenyUsers *")
	if len(sshReadOnly) > 0 {
		line("Match LocalPort 23 User %s", strings.Join(sshReadOnly, ","))
		line("\tForceCommand %s storage-shell -ro", agentBinary)
	}
	line("Match LocalPort 23")
	line("\tForceCommand %s storage-shell", agentBinary)
	if len(writable) > 0 {
		line("Match LocalPort 22 User %s", strings.Join(writable, ","))
		line("\tForceCommand internal-sftp -d %s", chrootHome)
	}
	return c.String()
}
