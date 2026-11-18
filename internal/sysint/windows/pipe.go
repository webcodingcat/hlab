//go:build windows

package windows

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"

	"github.com/webcodingcat/hlab/internal/profile"
)

// pipeSDDL gives SYSTEM and Administrators full access and Interactive
// Users read and write, protected from inheritance.
const pipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)"

// ListenManagement listens on the management pipe of the current profile.
func ListenManagement() (net.Listener, error) {
	return winio.ListenPipe(profile.Current().PipeName, &winio.PipeConfig{SecurityDescriptor: pipeSDDL})
}

// DialManagement connects to the management pipe of the current profile.
func DialManagement(ctx context.Context) (net.Conn, error) {
	return winio.DialPipeContext(ctx, profile.Current().PipeName)
}
