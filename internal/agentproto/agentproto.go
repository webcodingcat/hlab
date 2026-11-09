// Package agentproto is the wire format between hlabd and hlab-agent: HTTP
// with JSON bodies over hv_sock (Windows) or AF_VSOCK (Linux), and the
// pictures the agent applies. It holds types and their checks, no I/O.
//
// The service always sends a whole picture, never a change. The agent checks
// it first (Check) and refuses a broken one; the picture it applied before
// then stays.
package agentproto

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Port is the vsock port the agent listens on. Hyper-V reaches it through
// hv_sock with a service GUID made from the port, which the installer
// registers.
const Port = 5000

// The agent's routes.
const (
	// PathStatus answers GET with a Status; hlabd uses it as the ping.
	PathStatus = "/v1/status"
	// PathPictures takes a picture with PUT on PathPictures + its name. The
	// answer is 204 when it is applied. 400 means the body is not the
	// picture, 422 that the agent refused it and nothing changed, 500 that
	// applying it failed part way; hlabd then sends it again.
	PathPictures = "/v1/pictures/"
	// PathFormat takes a Format with POST: the agent makes a file system
	// on a disk that hlabd attached to the appliance for it, a volume
	// created with a format. It is the agent's one command besides the
	// pictures: a file system is made once, so there is no state to
	// picture. The answer is 204 when it is made, 422 for a Format its
	// check refuses or a disk it does not find.
	PathFormat = "/v1/format"
)

// Format names the disk to format by the WWN it shows the appliance, and
// the file system to make on it.
type Format struct {
	WWN        string `json:"wwn"` // as udev writes it: "0x6002248…"
	FileSystem string `json:"file_system"`
}

// The file systems a volume may have, as on Hetzner.
const (
	FileSystemExt4 = "ext4"
	FileSystemXFS  = "xfs"
)

// Check refuses a file system other than ext4 and xfs, and a WWN that is
// not "0x" and 32 hex digits.
func (f Format) Check() error {
	if f.FileSystem != FileSystemExt4 && f.FileSystem != FileSystemXFS {
		return fmt.Errorf("unknown file system %q", f.FileSystem)
	}
	digits, found := strings.CutPrefix(f.WWN, "0x")
	if _, err := hex.DecodeString(digits); !found || err != nil || len(digits) != 32 {
		return fmt.Errorf("bad WWN %q", f.WWN)
	}
	return nil
}

// The names of the pictures, in PathPictures and Status.Generations.
const (
	PictureLinks    = "links"
	PictureRuleset  = "ruleset"
	PictureDHCP     = "dhcp"
	PictureMetadata = "metadata"
)

// Status is what the agent reports about itself.
type Status struct {
	Version string `json:"version"`
	// Generations holds the generation of each picture applied since the
	// agent started; a picture it has not applied yet is missing. After a
	// restart of the agent hlabd sends every picture again.
	Generations map[string]uint64 `json:"generations"`
}

// Error is the body of every answer that is not a success.
type Error struct {
	Message string `json:"message"`
}
