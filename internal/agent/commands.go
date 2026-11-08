//go:build linux

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// commandTimeout bounds nft, dnsmasq and systemctl; each takes milliseconds.
const commandTimeout = 30 * time.Second

// dhcpFile is dnsmasq's configuration in the agent's folder.
const dhcpFile = "dnsmasq.conf"

// applyRuleset loads the ruleset with nft -f, which applies it as one
// transaction: a ruleset nft refuses changes nothing.
func applyRuleset(ruleset agentproto.Ruleset) error {
	if output, err := run(strings.NewReader(ruleset.Text), "nft", "-f", "-"); err != nil {
		return refused(fmt.Errorf("nft: %w: %s", err, output))
	}
	return nil
}

// applyDHCP writes dnsmasq's configuration and restarts it, if the
// configuration changed. dnsmasq reads its ranges and interfaces only when
// it starts, so a SIGHUP would not do.
func (a *Agent) applyDHCP(dhcp agentproto.DHCP) error {
	path := filepath.Join(a.opts.Dir, dhcpFile)
	if current, err := os.ReadFile(path); err == nil && string(current) == dhcp.Config {
		return nil
	}
	next := path + ".next"
	if err := os.WriteFile(next, []byte(dhcp.Config), 0o644); err != nil {
		return err
	}
	if output, err := run(nil, "dnsmasq", "--test", "--conf-file="+next); err != nil {
		return refused(errors.Join(fmt.Errorf("dnsmasq: %w: %s", err, output), os.Remove(next)))
	}
	if err := os.Rename(next, path); err != nil {
		return err
	}
	if output, err := run(nil, a.opts.RestartDHCP[0], a.opts.RestartDHCP[1:]...); err != nil {
		// without the file, the same picture sent again restarts it again
		return errors.Join(fmt.Errorf("restarting dnsmasq: %w: %s", err, output), os.Remove(path))
	}
	return nil
}

// run runs a command with stdin and returns its output.
func run(stdin *strings.Reader, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return strings.TrimSpace(output.String()), err
}
