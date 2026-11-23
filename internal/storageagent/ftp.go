package storageagent

import (
	"fmt"
	"path"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// FTPFiles is the vsftpd configuration of the storage VM.
type FTPFiles struct {
	Conf   string            // /etc/vsftpd.conf
	Users  map[string]string // user_config_dir file per account
	Access string            // /etc/security/vsftpd-access.conf (pam_access)
}

// Where the FTP service keeps its files.
const (
	vsftpdConf      = "/etc/vsftpd.conf"
	vsftpdUsersDir  = "/etc/vsftpd/users"
	vsftpdAccessCfg = "/etc/security/vsftpd-access.conf"
)

// FTPConfig returns the vsftpd configuration for doc, leaving out the
// accounts in skipped and the subaccounts of a skipped box; the
// certificate and key are storagebox.crt and storagebox.key in tlsDir.
//
// vsftpd listens on port 21 of every address and chroots each account to
// its home; the passive ports are 50000-50099 and the passive address is
// the one the client connected to. Explicit FTPS uses the wildcard
// certificate, plain FTP stays allowed. A read-only account gets
// write_enable=NO in its own file. pam_access admits an account that is
// not reachable externally only from the public range outside the host's
// transit network; the first matching rule decides.
func FTPConfig(doc agentapi.Storage, skipped map[string]bool, tlsDir string) FTPFiles {
	files := FTPFiles{Users: map[string]string{}}

	var c strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&c, format+"\n", args...) }
	line("# Written by hlab-agent (role storage); every apply replaces it.")
	line("listen=YES")
	line("listen_ipv6=NO")
	line("anonymous_enable=NO")
	line("local_enable=YES")
	line("write_enable=YES")
	line("local_umask=022")
	line("pam_service_name=vsftpd")
	line("secure_chroot_dir=/var/run/vsftpd/empty")
	line("user_sub_token=$USER")
	line("local_root=%s/$USER/home", chrootBase)
	line("chroot_local_user=YES")
	line("allow_writeable_chroot=YES")
	line("user_config_dir=%s", vsftpdUsersDir)
	line("connect_from_port_20=NO")
	line("pasv_enable=YES")
	line("pasv_min_port=50000")
	line("pasv_max_port=50099")
	line("ssl_enable=YES")
	line("rsa_cert_file=%s", path.Join(tlsDir, "storagebox.crt"))
	line("rsa_private_key_file=%s", path.Join(tlsDir, "storagebox.key"))
	line("allow_anon_ssl=NO")
	line("ssl_sslv2=NO")
	line("ssl_sslv3=NO")
	line("ssl_tlsv1=NO")
	line("ssl_tlsv11=NO")
	line("ssl_tlsv12=YES")
	line("ssl_tlsv13=YES")
	line("ssl_ciphers=ECDHE+AESGCM:ECDHE+CHACHA20")
	line("require_ssl_reuse=NO")
	line("force_local_logins_ssl=NO")
	line("force_local_data_ssl=NO")
	files.Conf = c.String()

	var a strings.Builder
	a.WriteString("# Written by hlab-agent (role storage); every apply replaces it.\n")
	internal := doc.PublicRange.Masked().String() + " EXCEPT " + doc.HostNet.Masked().String()
	account := func(user string, acc agentapi.Access) {
		if acc.ReadOnly {
			files.Users[user] = "write_enable=NO\n"
		} else {
			files.Users[user] = "write_enable=YES\n"
		}
		if !acc.ReachableExternally {
			fmt.Fprintf(&a, "+:%s:%s\n", user, internal)
			fmt.Fprintf(&a, "-:%s:ALL\n", user)
		}
	}
	for _, b := range doc.Boxes {
		if skipped[b.User] {
			continue
		}
		account(b.User, agentapi.Access{ReachableExternally: b.Access.ReachableExternally})
		for _, s := range b.Subaccounts {
			if !skipped[s.User] {
				account(s.User, s.Access)
			}
		}
	}
	a.WriteString("+:ALL:ALL\n")
	files.Access = a.String()
	return files
}
