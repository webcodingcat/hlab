// Package agentproto is the wire format between hlabd and hlab-agent: HTTP
// with JSON bodies over hv_sock (Windows) or AF_VSOCK (Linux), and the
// pictures the agent applies. It holds types and their checks, no I/O.
//
// The service always sends a whole picture, never a change. The agent checks
// it first (Check) and refuses a broken one; the picture it applied before
// then stays.
package agentproto

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
)

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
