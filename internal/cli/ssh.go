package cli

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newSSH() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "ssh SERVER [COMMAND...]",
		Short: "Log into a server as root with the lab's key",
		Long: `Log into a server as root with the lab's own SSH key, which every server
takes besides its own keys, or run a command there. The server is named by
its name or ID; it is reached on its Primary IPv4, else its IPv6.

  hlab ssh web
  hlab ssh --project shop web journalctl -u nginx

Needs ssh (OpenSSH) on the PATH; works on Windows and in WSL2.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, err := connect()
			if err != nil {
				return err
			}
			access, err := lab.SSHAccess(project, args[0])
			if err != nil {
				return err
			}
			// ssh takes a key only from a file that others cannot read:
			// the data folder's copy is readable for every local user
			keyFile, err := os.CreateTemp("", "hlab-key-*")
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(keyFile.Name()) }()
			if _, err := keyFile.WriteString(access.PrivateKey); err != nil {
				_ = keyFile.Close()
				return err
			}
			if err := keyFile.Close(); err != nil {
				return err
			}
			sshArgs := []string{
				"-i", keyFile.Name(), "-o", "IdentitiesOnly=yes",
				// a server's address comes back with a new host key after
				// each create, so known_hosts would only get in the way
				"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=" + os.DevNull,
				"-o", "LogLevel=ERROR",
				access.User + "@" + access.Address,
			}
			ssh := exec.Command("ssh", append(sshArgs, args[1:]...)...)
			ssh.Stdin, ssh.Stdout, ssh.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
			return ssh.Run()
		},
	}
	cmd.Flags().StringVar(&project, "project", "default", "the server's project")
	// what follows the server is the command, with its own flags
	cmd.Flags().SetInterspersed(false)
	return cmd
}
