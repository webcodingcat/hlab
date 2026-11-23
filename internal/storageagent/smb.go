package storageagent

import (
	"fmt"
	"net/netip"
	"path"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// SambaFiles is the Samba configuration of the storage VM.
type SambaFiles struct {
	Global   string                // /etc/samba/smb.conf
	PerIP    map[netip.Addr]string // /etc/samba/ip-<addr>.conf, shares of that box IP
	Accounts []string              // accounts that get a tdbsam entry (samba on, not skipped)
}

// sambaIncludeDir holds smb.conf and the per-IP includes.
const sambaIncludeDir = "/etc/samba"

// SambaConfig returns the Samba configuration for doc, leaving out the
// accounts in skipped and the subaccounts of a skipped box.
//
// smbd listens on port 445 of the box IPs only and includes the file of
// the address a client connected to (E16), so every box IP serves its own
// shares: "backup" with the box's data for a box with Samba on, and a
// share named after each subaccount with Samba on, with its home. Each
// share admits its one account; a read-only subaccount's share is read
// only, and an account that is not reachable externally is admitted only
// from the public range outside the host's transit network.
func SambaConfig(doc agentapi.Storage, skipped map[string]bool) SambaFiles {
	files := SambaFiles{PerIP: map[netip.Addr]string{}}
	var ips []string
	internal := ""
	if doc.PublicRange.IsValid() {
		internal = doc.PublicRange.Masked().String()
		if doc.HostNet.IsValid() {
			internal += " EXCEPT " + doc.HostNet.Masked().String()
		}
	}
	for _, b := range doc.Boxes {
		if skipped[b.User] || !b.IP.IsValid() {
			continue
		}
		ips = append(ips, b.IP.String())
		var c strings.Builder
		fmt.Fprintf(&c, "# Shares of %s, written by hlab-agent (role storage); every apply replaces it.\n", b.User)
		share := func(name, user string, a agentapi.Access) {
			files.Accounts = append(files.Accounts, user)
			fmt.Fprintf(&c, "\n[%s]\n", name)
			fmt.Fprintf(&c, "\tpath = %s\n", path.Join(chrootBase, user, "home"))
			fmt.Fprintf(&c, "\tvalid users = %s\n", user)
			if a.ReadOnly {
				c.WriteString("\tread only = yes\n")
			} else {
				c.WriteString("\tread only = no\n")
			}
			if !a.ReachableExternally && internal != "" {
				fmt.Fprintf(&c, "\thosts allow = %s\n", internal)
			}
		}
		if b.Access.Samba {
			share("backup", b.User, agentapi.Access{ReachableExternally: b.Access.ReachableExternally})
		}
		for _, s := range b.Subaccounts {
			if s.Access.Samba && !skipped[s.User] {
				share(s.User, s.User, s.Access)
			}
		}
		files.PerIP[b.IP] = c.String()
	}
	// Without a box smbd listens on the loopback interface only.
	interfaces := "lo"
	if len(ips) > 0 {
		interfaces = strings.Join(ips, " ")
	}

	var g strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&g, format+"\n", args...) }
	line("# Written by hlab-agent (role storage); every apply replaces it.")
	line("[global]")
	line("\tserver role = standalone server")
	line("\tsecurity = user")
	line("\tpassdb backend = tdbsam")
	line("\tworkgroup = WORKGROUP")
	line("\tserver min protocol = SMB2_10")
	line("\tsmb ports = 445")
	line("\tdisable netbios = yes")
	line("\tmap to guest = never")
	line("\trestrict anonymous = 2")
	line("\tdurable handles = no")
	line("\tload printers = no")
	line("\tprinting = bsd")
	line("\tprintcap name = /dev/null")
	line("\tdisable spoolss = yes")
	line("\tlog file = /var/log/samba/log.%%m")
	line("\tmax log size = 1000")
	line("\tbind interfaces only = yes")
	line("\tinterfaces = %s", interfaces)
	// Last: the shares of the address the client connected to.
	line("\tinclude = %s/ip-%%i.conf", sambaIncludeDir)
	files.Global = g.String()
	return files
}
