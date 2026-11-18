// Package appliance holds the boot files of the hlab appliance VM.
//
// The appliance's cidata ISO (volume label "cidata", written by hlabd at
// every cloud start) must contain:
//
//   - user-data: UserData()
//   - meta-data: instance-id and local-hostname
//   - network-config: v2, eth0 198.19.0.1/30 with default route via 198.19.0.2
//   - AgentFile ("hlab-agent"): the static Linux binary bin/hlab-agent
//   - SecretFile ("secret"): the agent secret, one line
//   - <name>.deb: the packages of catalog/appliance-debs.json (xfsprogs and
//     its libraries), installed by the install script while mkfs.xfs is
//     missing
//
// On first boot cloud-init installs the systemd unit hlab-agent.service and
// the install script; at every boot the unit copies AgentFile to
// /usr/local/sbin/hlab-agent and SecretFile to /etc/hlab-agent/secret (mode
// 0600) from the ISO and starts the agent, so a cloud start with a new ISO
// updates the agent.
package appliance

import (
	_ "embed"
	"encoding/base64"
	"strings"
)

// Names of the agent's files on the cidata ISO.
const (
	AgentFile  = "hlab-agent"
	SecretFile = "secret"
)

var (
	//go:embed hlab-agent.service
	unit string
	//go:embed hlab-agent-install.sh
	installScript string
	//go:embed user-data.yaml
	userData string
)

// UserData returns the appliance's cloud-init user-data.
func UserData() []byte {
	enc := base64.StdEncoding.EncodeToString
	return []byte(strings.NewReplacer(
		"@INSTALL_B64@", enc([]byte(installScript)),
		"@UNIT_B64@", enc([]byte(unit)),
	).Replace(userData))
}
