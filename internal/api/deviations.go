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
			Description: "root_password is always null; password login is not supported yet",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/request_console",
			Description: "answers unsupported_error: server console is not supported by hlab yet",
		},
	}
}
