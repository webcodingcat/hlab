package control

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/webcodingcat/hlab/internal/labapi"
	"github.com/webcodingcat/hlab/internal/state"
)

// sshAccess answers what `hlab ssh` needs: the server's address (its
// Primary IPv4, else the ::1 of its IPv6 network, as Hetzner's guests take
// it) and the lab's private key, which every server's root takes. The
// server is named by its name or ID in the project.
func (c *Control) sshAccess(r *http.Request) (int, any, error) {
	if len(c.opts.LabKey) == 0 {
		return 0, nil, errors.New("the lab has no SSH key")
	}
	project, err := c.opts.Lab.ProjectByName(r.PathValue("project"))
	if err != nil {
		return 0, nil, err
	}
	nameOrID := r.PathValue("server")
	access := labapi.SSHAccess{User: "root", PrivateKey: string(c.opts.LabKey)}
	err = c.opts.Lab.View(func(tx *state.Tx) error {
		server, found := state.Find(tx, state.Servers, func(server *state.Server) bool {
			return server.ProjectID == project.ID &&
				(server.Name == nameOrID || strconv.FormatInt(server.ID, 10) == nameOrID)
		})
		if !found {
			return fmt.Errorf("no server %s in the project %s: %w", nameOrID, project.Name, state.ErrNotFound)
		}
		access.Server = server.Name
		if ip, err := state.Get(tx, state.PrimaryIPs, server.IPv4); err == nil {
			access.Address = ip.IP
			return nil
		}
		if ip, err := state.Get(tx, state.PrimaryIPs, server.IPv6); err == nil {
			if network, err := netip.ParsePrefix(ip.IP); err == nil {
				access.Address = network.Addr().Next().String()
				return nil
			}
		}
		return fmt.Errorf("the server %s has no Primary IP: %w", server.Name, state.ErrNotFound)
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, access, nil
}
