package cli

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// envTokenName is the name of the token that `hlab env` makes in a project;
// each call replaces it, so that the tokens do not pile up.
const envTokenName = "hlab-env"

// The shells `hlab env` writes for.
const (
	shellSh         = "sh"
	shellPowerShell = "powershell"
)

func newEnv() *cobra.Command {
	var project, shell string
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Print the variables that point the tools at HLab",
		Long: `Print the variables that point the tools (hcloud, OpenTofu, Ansible) at HLab:
the endpoints, a new read_write token of the project and the web UI.

  eval "$(hlab env)"                      sh, bash, zsh
  hlab env --shell powershell | iex       PowerShell

Each call makes a new token named ` + envTokenName + ` and deletes the one before:
a shell that got the earlier token needs to run hlab env again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if shell != shellSh && shell != shellPowerShell {
				return fmt.Errorf("unknown shell %q: use %s or %s", shell, shellSh, shellPowerShell)
			}
			lab, err := connect()
			if err != nil {
				return err
			}
			status, err := lab.Status()
			if err != nil {
				return err
			}
			created, err := lab.CreateToken(project, envTokenName, "read_write", true)
			if err != nil {
				return err
			}
			for _, variable := range [][2]string{
				{"HCLOUD_ENDPOINT", status.Endpoints.Cloud},
				{"HETZNER_ENDPOINT", status.Endpoints.Hetzner},
				{"HCLOUD_TOKEN", created.Secret},
				{"HLAB_UI", status.Endpoints.WebUI},
			} {
				cmd.Println(exportLine(shell, variable[0], variable[1]))
			}
			return nil
		},
	}
	defaultShell := shellSh
	if runtime.GOOS == "windows" {
		defaultShell = shellPowerShell
	}
	cmd.Flags().StringVar(&project, "project", "default", "the project whose token to print")
	cmd.Flags().StringVar(&shell, "shell", defaultShell, "the shell to write for: sh or powershell")
	return cmd
}

// exportLine returns the line that sets an environment variable in a shell,
// with the value in single quotes, which neither shell expands.
func exportLine(shell, name, value string) string {
	if shell == shellPowerShell {
		return "$env:" + name + " = '" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "export " + name + "='" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func newStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether HLab runs, and where",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lab, err := connect()
			if err != nil {
				return err
			}
			status, err := lab.Status()
			if err != nil {
				return err
			}
			cmd.Printf("HLab %s: %s since %s\n", status.Version, status.State, localTime(status.Started))
			return printTable(cmd, [][]string{
				{"platform", status.Platform},
				{"data folder", status.DataDir},
				{"projects", strconv.Itoa(status.Projects)},
				{"Cloud API", status.Endpoints.Cloud},
				{"Hetzner API", status.Endpoints.Hetzner},
				{"web UI", status.Endpoints.WebUI},
			})
		},
	}
}

func newStop() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop HLab",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lab, err := connect()
			if err != nil {
				return err
			}
			if err := lab.Shutdown(); err != nil {
				return err
			}
			cmd.Println("HLab is stopping")
			return nil
		},
	}
}
