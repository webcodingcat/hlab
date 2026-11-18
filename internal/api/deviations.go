package api

// Deviation is one known difference between hlab and the Hetzner Cloud API.
type Deviation struct {
	Endpoint    string
	Description string
}

// Deviations lists the known differences from the Hetzner Cloud API; the
// user documentation names each of them.
func Deviations() []Deviation {
	return []Deviation{
		{
			Endpoint:    "POST /servers",
			Description: "the IPv6 /64 is assigned formally; the server gets no IPv6 traffic",
		},
		{
			Endpoint:    "POST /servers",
			Description: "the returned root password does not expire; Hetzner forces a change at the first login",
		},
		{
			Endpoint:    "POST /servers",
			Description: "resource_unavailable when the host lacks CPUs, RAM or disk beyond the configured reserve",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/shutdown",
			Description: "the action succeeds once the request is sent to the guest, not when the server is off",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/rebuild",
			Description: "accepts system images only",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/reset_password",
			Description: "works through the hlab guest hook, not qemu-guest-agent; 409 conflict when the server is not running",
		},
		{
			Endpoint:    "GET /servers/{id}/metrics",
			Description: "samples every 10 s kept 24 h in memory, lost at cloud stop; disk.0 sums all disks of the VM",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/request_console",
			Description: "answers unsupported_error: server console is not supported by hlab yet",
		},
		{
			Endpoint:    "http://169.254.169.254/hetzner/v1/metadata",
			Description: "no EC2 routes; public-keys and userdata in cloud-init's shape",
		},
	}
}
