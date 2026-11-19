package servers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/store"
)

// DNSPtrs is the reverse DNS hook of change_dns_ptr (set by the cloud; nil:
// change_dns_ptr answers 422 invalid_input on "ip").
type DNSPtrs interface {
	// ChangeServer applies change_dns_ptr on the primary IP of server r that
	// holds ip, updates r.PublicNet and returns the primary IP's ID.
	ChangeServer(tx store.Tx, r *Record, ip string, ptr *string) (int64, error)
}

// SetDNSPtrs sets the reverse DNS hook.
func (m *Module) SetDNSPtrs(h DNSPtrs) { m.dnsPtrs = h }

// DecodeDNSPtr reads the body of a change_dns_ptr action: ip and dns_ptr
// are required, dns_ptr may be null (nil).
func DecodeDNSPtr(w http.ResponseWriter, req *http.Request) (ip string, ptr *string, ok bool) {
	var body struct {
		IP     string          `json:"ip"`
		DNSPtr json.RawMessage `json:"dns_ptr"`
	}
	if !api.Decode(w, req, &body) {
		return "", nil, false
	}
	if len(body.DNSPtr) == 0 {
		api.Fail(w, &api.FieldError{Field: "dns_ptr", Message: "dns_ptr is required"})
		return "", nil, false
	}
	if err := json.Unmarshal(body.DNSPtr, &ptr); err != nil {
		api.Fail(w, &api.FieldError{Field: "dns_ptr", Message: "dns_ptr must be a string or null"})
		return "", nil, false
	}
	return body.IP, ptr, true
}

// changeDNSPtr sets or resets the reverse DNS entry of one of the server's
// public IPs through its primary IP; the action pushes the agent document.
func (m *Module) changeDNSPtr(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	ip, ptr, ok := DecodeDNSPtr(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		if m.dnsPtrs == nil {
			return &api.FieldError{Field: "ip", Message: "the server holds no primary IP with this address"}
		}
		pipID, err := m.dnsPtrs.ChangeServer(tx, &r, ip, ptr)
		if err != nil {
			return err
		}
		refs := []actions.Ref{{ID: id, Type: refType}, {ID: pipID, Type: "primary_ip"}}
		if action, err = acts.Start(tx, p.ID, "change_dns_ptr", refs); err != nil {
			return err
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error { return m.d.PushAgent(ctx) })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// showPublic returns pn as the API shows it: an IPv4 without stored PTR
// shows the default, an IPv6 without entries an empty list.
func showPublic(pn PublicNet) PublicNet {
	if pn.IPv4 != nil && pn.IPv4.DNSPtr == "" {
		v4 := *pn.IPv4
		if a, err := netip.ParseAddr(v4.IP); err == nil {
			v4.DNSPtr = rdns.Default(a)
		}
		pn.IPv4 = &v4
	}
	if pn.IPv6 != nil && pn.IPv6.DNSPtr == nil {
		v6 := *pn.IPv6
		v6.DNSPtr = []rdns.Entry{}
		pn.IPv6 = &v6
	}
	return pn
}
