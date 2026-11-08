package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/webcodingcat/hlab/internal/client"
	"github.com/webcodingcat/hlab/internal/labapi"
)

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
the endpoints, the project's read_write token named hlab-env and the web UI.

  eval "$(hlab env)"                      sh, bash, zsh
  hlab env --shell powershell | iex       PowerShell

The token stays the same across calls, shells and restarts of HLab; deleting
it (hlab token delete hlab-env) makes the next call create a new one.`,
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
			token, err := lab.EnvToken(project)
			if err != nil {
				return err
			}
			for _, variable := range [][2]string{
				{"HCLOUD_ENDPOINT", status.Endpoints.Cloud},
				{"HETZNER_ENDPOINT", status.Endpoints.Hetzner},
				{"HCLOUD_TOKEN", token},
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

// installedHLabd is where install.ps1 puts hlabd; `hlab start` runs it
// from WSL2.
const installedHLabd = `C:\Program Files\HLab\hlabd.exe`

// How long `hlab start` and `hlab stop` wait. A cold start with the
// appliance takes about 20 s; a stop gives the servers shutdown_timeout
// (60 s by default) and the appliance 30 s.
const (
	startTimeout = 3 * time.Minute
	stopTimeout  = 10 * time.Minute
)

// pollInterval is how often `hlab start` and `hlab stop` ask the lab.
var pollInterval = 500 * time.Millisecond

func newStart() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start HLab; Windows asks for administrator rights",
		Long: `Start HLab with administrator rights, which Windows asks for (UAC), and wait
until it answers. On Windows it starts the hlabd next to hlab; in WSL2 the
installed one (` + installedHLabd + `), with the default data folder.
hlabd runs without a window: hlab stop ends it, and its log is
logs\hlabd.log in the data folder.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if status, err := currentStatus(); err == nil {
				cmd.Printf("HLab runs already, with the data folder %s\n", status.DataDir)
				return nil
			}
			hlabd, err := hlabdPath()
			if err != nil {
				return err
			}
			if err := startElevated(hlabd); err != nil {
				return err
			}
			cmd.Println("HLab is starting")
			running := func() bool {
				status, err := currentStatus()
				return err == nil && status.State == labapi.StateRunning
			}
			if !waitUntil(startTimeout, running) {
				return fmt.Errorf("HLab did not answer within %s; its log is in %s",
					startTimeout, filepath.Join(client.DataDir(), "logs"))
			}
			status, err := currentStatus()
			if err != nil {
				return err
			}
			cmd.Printf("HLab %s runs, with the data folder %s\n", status.Version, status.DataDir)
			return nil
		},
	}
}

// hlabdPath returns the hlabd that `hlab start` runs.
func hlabdPath() (string, error) {
	switch {
	case runtime.GOOS == "windows":
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Dir(executable), "hlabd.exe"), nil
	case client.InWSL():
		return installedHLabd, nil
	default:
		return "", errors.New("hlab start runs on Windows and in WSL2; on Linux, start hlabd yourself")
	}
}

// startElevated starts a Windows program with administrator rights and
// without a window, through Windows PowerShell, which WSL2 reaches through
// interop as well. It returns once Windows has started it, or the user
// declined.
func startElevated(program string) error {
	script := "Start-Process -FilePath '" + strings.ReplaceAll(program, "'", "''") +
		"' -Verb RunAs -WindowStyle Hidden"
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		reason := strings.TrimSpace(string(output))
		if reason == "" {
			reason = err.Error()
		}
		reason, _, _ = strings.Cut(reason, "\n")
		return fmt.Errorf("starting %s with administrator rights: %s", program, strings.TrimSpace(reason))
	}
	return nil
}

// currentStatus returns the status of the lab that runs now; it reads the
// token file anew, which a starting lab writes.
func currentStatus() (labapi.Status, error) {
	lab, err := connect()
	if err != nil {
		return labapi.Status{}, err
	}
	return lab.Status()
}

// waitUntil asks done every pollInterval until it says yes or the timeout
// has passed, and tells which came first.
func waitUntil(timeout time.Duration, done func() bool) bool {
	deadline := time.Now().Add(timeout)
	for !done() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
	return true
}

func newStop() *cobra.Command {
	var noWait bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop HLab and wait until it has stopped",
		Long: `Stop HLab: the servers shut down (shutdown_timeout in hlab.yaml, then they are
turned off), then the appliance and the host link go. hlab stop waits until
HLab has stopped, unless --no-wait.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lab, err := connect()
			if err != nil {
				return err
			}
			if err := lab.Shutdown(); err != nil {
				return err
			}
			cmd.Println("HLab is stopping")
			if noWait {
				return nil
			}
			gone := func() bool {
				_, err := currentStatus()
				return errors.Is(err, client.ErrNotRunning)
			}
			if !waitUntil(stopTimeout, gone) {
				return fmt.Errorf("HLab did not stop within %s", stopTimeout)
			}
			cmd.Println("HLab has stopped")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return once HLab has begun to stop")
	return cmd
}
