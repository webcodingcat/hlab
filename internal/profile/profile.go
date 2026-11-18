// Package profile names everything that could collide when the release and
// the dev build of hlab are installed on one machine.
package profile

// Name is the profile of this binary, set at link time with
// -ldflags "-X github.com/webcodingcat/hlab/internal/profile.Name=release".
var Name = "dev"

// Profile holds the names of one profile.
type Profile struct {
	Name        string // "release" or "dev"
	ServiceName string // Windows service
	PipeName    string // management named pipe
	DataDirName string // folder below %ProgramData%
	Tag         string // switch, VM name prefix, NAT name, NRPT comment, firewall rule group
}

var (
	release = Profile{
		Name:        "release",
		ServiceName: "hlab",
		PipeName:    `\\.\pipe\hlab`,
		DataDirName: "hlab",
		Tag:         "hlab",
	}
	dev = Profile{
		Name:        "dev",
		ServiceName: "hlab-dev",
		PipeName:    `\\.\pipe\hlab-dev`,
		DataDirName: "hlab-dev",
		Tag:         "hlab-dev",
	}
)

// Current returns the profile this binary was built for. Any Name other
// than "release" gives the dev profile.
func Current() Profile {
	if Name == release.Name {
		return release
	}
	return dev
}

// Other returns the profile this binary was not built for.
func Other() Profile {
	if Current().Name == release.Name {
		return dev
	}
	return release
}
