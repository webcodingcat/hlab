//go:build windows

package windows

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
)

// hvsockDialer reaches the agent inside a VM over hv_sock: Hyper-V addresses
// the VM by its ID and the agent's vsock port by a service ID made from the
// port (winio.VsockServiceID). The service ID must be registered once, as
// administrator (bin/dev.ps1 dev-install, later the installer); then any
// member of Hyper-V Administrators may dial.
type hvsockDialer struct {
	hypervisor *Hypervisor
}

// Dial connects to the port in the VM. A VM that is missing fails with
// ErrNotFound.
func (d hvsockDialer) Dial(ctx context.Context, vm string, port uint32) (net.Conn, error) {
	id, err := d.hypervisor.vmID(ctx, vm)
	if err != nil {
		return nil, err
	}
	vmID, err := guid.FromString(id)
	if err != nil {
		return nil, fmt.Errorf("the ID %q of VM %s: %w", id, vm, err)
	}
	address := &winio.HvsockAddr{VMID: vmID, ServiceID: winio.VsockServiceID(port)}
	conn, err := (&winio.HvsockDialer{}).Dial(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("hv_sock to port %d of VM %s: %w", port, vm, err)
	}
	return conn, nil
}
