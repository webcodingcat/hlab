// Package cli holds the commands of `hlab`, the user's command line. Each
// command calls the lab control of the running hlabd through package client.
package cli

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/webcodingcat/hlab/internal/client"
	"github.com/webcodingcat/hlab/internal/config"
)

// hostAddress is where hlabd answers on the host link, the same address on
// Windows, in WSL2 and for guests.
const hostAddress = "198.18.0.1"

// Execute runs the command line args (without the program's name) and
// returns the exit code.
func Execute(args []string, stdout, stderr io.Writer, version string) int {
	root := newRoot(version)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		root.PrintErrln("hlab:", err)
		var answerErr *client.Error
		switch {
		case errors.Is(err, client.ErrNotRunning):
			root.PrintErrln("hlab: start hlabd first; with another data folder or address, set",
				client.TokenFileVar, "and", client.ControlVar)
		case errors.As(err, &answerErr) && answerErr.Status == http.StatusUnauthorized:
			// the token file belongs to another hlabd than the one that answered
			root.PrintErrln("hlab: the control token does not fit this hlabd; set",
				client.TokenFileVar, "to the token of the hlabd at", client.ControlVar)
		}
		return 1
	}
	return 0
}

// printTable prints rows as a table with aligned columns.
func printTable(cmd *cobra.Command, rows [][]string) error {
	table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, row := range rows {
		if _, err := io.WriteString(table, strings.Join(row, "\t")+"\n"); err != nil {
			return err
		}
	}
	return table.Flush()
}

// newRoot returns the command `hlab` with every subcommand.
func newRoot(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "hlab",
		Short:         "Control HLab, a local stand-in for Hetzner Cloud",
		Version:       version,
		SilenceErrors: true, // Execute prints them, with a hint
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("hlab {{.Version}}\n") // as hlabd --version prints it
	root.AddCommand(newEnv(), newStatus(), newStop(), newProject(), newToken())
	return root
}

// connect returns a client of the running lab's control.
func connect() (*client.Client, error) {
	return client.FromEnvironment("http://" + hostAddress + ":" + strconv.Itoa(config.Default().Ports.Control))
}
