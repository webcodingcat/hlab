package storageagent

import (
	"fmt"
	"path"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// WebDAVFiles is the Apache configuration of the storage VM.
type WebDAVFiles struct {
	Site   string            // the one Apache site: Listen and vhosts of every box IP
	Passwd map[string]string // account -> htpasswd line content file (crypt hash)
}

// Where the WebDAV service keeps its files: the passwd file of an account
// is <webdavPasswdDir>/<account>, the lock database of an account
// <davLockDir>/<box>/<account> in a folder of the box user.
const (
	webdavPasswdDir = "/etc/apache2/hlab-dav"
	davLockDir      = "/var/lib/hlab/davlock"
)

// WebDAVConfig returns the Apache configuration for doc, leaving out the
// accounts in skipped and the subaccounts of a skipped box; the
// certificate and key are storagebox.crt and storagebox.key in tlsDir.
//
// Apache listens on port 443 of every box IP. The first vhost of an IP is
// a catch-all that refuses every request, so an unknown name is not
// served by an account; then each account with WebDAV on gets a vhost
// named after it under storagebox.hetzner.internal that runs as the box
// user (mpm-itk), serves the account's data with basic authentication,
// limits a read-only account to reading methods and an account that is
// not reachable externally to the public range outside the host's transit
// network.
func WebDAVConfig(doc agentapi.Storage, skipped map[string]bool, tlsDir string) WebDAVFiles {
	files := WebDAVFiles{Passwd: map[string]string{}}
	var c strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&c, format+"\n", args...) }
	cert := path.Join(tlsDir, "storagebox.crt")
	key := path.Join(tlsDir, "storagebox.key")

	line("# Written by hlab-agent (role storage); every apply replaces it.")
	line("<IfModule mpm_prefork_module>")
	line("\tStartServers 2")
	line("\tMinSpareServers 2")
	line("\tMaxSpareServers 4")
	line("\tMaxRequestWorkers 20")
	line("</IfModule>")
	line("LimitXMLRequestBody 1000000")

	tls := func() {
		line("\tSSLEngine on")
		line("\tSSLCertificateFile %s", cert)
		line("\tSSLCertificateKeyFile %s", key)
	}
	for _, b := range doc.Boxes {
		if skipped[b.User] {
			continue
		}
		addr := b.IP.String() + ":443"
		line("")
		line("# Box %s", b.User)
		line("Listen %s", addr)
		line("<VirtualHost %s>", addr)
		line("\tServerName %s", b.IP)
		line("\tAssignUserID nobody nogroup")
		tls()
		line("\t<Location />")
		line("\t\tRequire all denied")
		line("\t</Location>")
		line("</VirtualHost>")

		vhost := func(user string, h agentapi.Hashes, a agentapi.Access) {
			files.Passwd[user] = user + ":" + h.Crypt + "\n"
			root := path.Join(chrootBase, user, "home")
			line("<VirtualHost %s>", addr)
			line("\tServerName %s.storagebox.hetzner.internal", user)
			line("\tAssignUserID %s %s", b.User, b.User)
			tls()
			line("\tDocumentRoot \"%s\"", root)
			line("\tDavLockDB %s", path.Join(davLockDir, b.User, user))
			line("\t<Directory \"%s\">", root)
			line("\t\tDav On")
			line("\t\tOptions None")
			line("\t\tAllowOverride None")
			line("\t\tAuthType Basic")
			line("\t\tAuthName \"%s\"", user)
			line("\t\tAuthBasicProvider file")
			line("\t\tAuthUserFile %s", path.Join(webdavPasswdDir, user))
			line("\t\t<RequireAll>")
			line("\t\t\tRequire valid-user")
			if a.ReadOnly {
				line("\t\t\tRequire method GET HEAD OPTIONS PROPFIND")
			}
			if !a.ReachableExternally {
				line("\t\t\tRequire ip %s", doc.PublicRange.Masked())
				line("\t\t\tRequire not ip %s", doc.HostNet.Masked())
			}
			line("\t\t</RequireAll>")
			line("\t</Directory>")
			line("</VirtualHost>")
		}
		if b.Access.WebDAV {
			vhost(b.User, b.Hashes, agentapi.Access{ReachableExternally: b.Access.ReachableExternally})
		}
		for _, s := range b.Subaccounts {
			if s.Access.WebDAV && !skipped[s.User] {
				vhost(s.User, s.Hashes, s.Access)
			}
		}
	}
	files.Site = c.String()
	return files
}
