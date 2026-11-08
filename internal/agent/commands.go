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

// dhcpFile returns the name of a dnsmasq instance's configuration in the
// agent's folder: one per device it serves.
func dhcpFile(device string) string {
	return "dnsmasq-" + device + ".conf"
}

// applyRuleset loads the ruleset with nft -f, which applies it as one
// transaction: a ruleset nft refuses changes nothing.
func applyRuleset(ruleset agentproto.Ruleset) error {
	if output, err := run(strings.NewReader(ruleset.Text), "nft", "-f", "-"); err != nil {
		return refused(fmt.Errorf("nft: %w: %s", err, output))
	}
	return nil
}

// applyDHCP makes the dnsmasq instances match the picture: it checks
// every new configuration with dnsmasq --test first, so that a picture
// dnsmasq refuses changes nothing; then it restarts the instances whose
// configuration changed and stops those of devices the picture lacks.
// dnsmasq reads its ranges and interfaces only when it starts, so a SIGHUP
// would not do.
func (a *Agent) applyDHCP(dhcp agentproto.DHCP) error {
	var changed []agentproto.DHCPInstance
	var problems []error
	for _, instance := range dhcp.Instances {
		path := filepath.Join(a.opts.Dir, dhcpFile(instance.Device))
		if current, err := os.ReadFile(path); err == nil && string(current) == instance.Config {
			continue
		}
		if err := os.WriteFile(path+".next", []byte(instance.Config), 0o644); err != nil {
			problems = append(problems, err)
			continue
		}
		changed = append(changed, instance)
		if output, err := run(nil, "dnsmasq", "--test", "--conf-file="+path+".next"); err != nil {
			problems = append(problems, refused(fmt.Errorf("dnsmasq for %s: %w: %s", instance.Device, err, output)))
		}
	}
	if len(problems) > 0 {
		for _, instance := range changed {
			problems = append(problems, os.Remove(filepath.Join(a.opts.Dir, dhcpFile(instance.Device))+".next"))
		}
		return errors.Join(problems...)
	}
	for _, instance := range changed {
		path := filepath.Join(a.opts.Dir, dhcpFile(instance.Device))
		if err := os.Rename(path+".next", path); err != nil {
			return err
		}
		restart := a.opts.RestartDHCP(instance.Device)
		if output, err := run(nil, restart[0], restart[1:]...); err != nil {
			// without the file, the same picture sent again restarts it again
			return errors.Join(fmt.Errorf("restarting dnsmasq for %s: %w: %s", instance.Device, err, output), os.Remove(path))
		}
	}
	return a.stopOtherDHCP(dhcp.Instances)
}

// stopOtherDHCP stops the dnsmasq instances of the devices that are not
// among instances and removes their configuration.
func (a *Agent) stopOtherDHCP(instances []agentproto.DHCPInstance) error {
	wanted := map[string]bool{}
	for _, instance := range instances {
		wanted[dhcpFile(instance.Device)] = true
	}
	files, err := filepath.Glob(filepath.Join(a.opts.Dir, dhcpFile("*")))
	if err != nil {
		return err
	}
	for _, path := range files {
		name := filepath.Base(path)
		if wanted[name] {
			continue
		}
		device := strings.TrimSuffix(strings.TrimPrefix(name, "dnsmasq-"), ".conf")
		stop := a.opts.StopDHCP(device)
		if output, err := run(nil, stop[0], stop[1:]...); err != nil {
			return fmt.Errorf("stopping dnsmasq for %s: %w: %s", device, err, output)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
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
