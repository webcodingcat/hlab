//go:build linux

package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"syscall"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// ListenMetadata listens for the metadata service on address, such as
// "169.254.169.254:80". The address may not exist yet: the links picture
// puts it on lo later (IP_FREEBIND).
func ListenMetadata(address string) (net.Listener, error) {
	config := net.ListenConfig{Control: func(_, _ string, conn syscall.RawConn) error {
		var optionErr error
		err := conn.Control(func(fd uintptr) {
			optionErr = syscall.SetsockoptInt(int(fd), syscall.SOL_IP, syscall.IP_FREEBIND, 1)
		})
		return errors.Join(err, optionErr)
	}}
	return config.Listen(context.Background(), "tcp", address)
}

// MetadataServer returns the metadata service's HTTP server: it notes the
// VRF each connection comes through (metadataConnContext).
func (a *Agent) MetadataServer() *http.Server {
	return &http.Server{Handler: a.MetadataHandler(), ConnContext: metadataConnContext}
}

// vrfKey is the context key of the VRF a request came through.
type vrfKey struct{}

// metadataConnContext notes the VRF a connection came through, "" for
// none: with tcp_l3mdev_accept, which the links picture sets, the listener
// accepts connections from the private networks' VRFs too, and the kernel
// binds each such connection to its VRF.
func metadataConnContext(ctx context.Context, conn net.Conn) context.Context {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return ctx
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return ctx
	}
	index := 0
	_ = raw.Control(func(fd uintptr) {
		index, _ = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, soBindToIfindex)
	})
	if index == 0 {
		return ctx
	}
	vrf, err := net.InterfaceByIndex(index)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, vrfKey{}, vrf.Name)
}

// soBindToIfindex is Linux's SO_BINDTOIFINDEX, which package syscall
// lacks: the index of the device a socket is bound to, 0 for none.
const soBindToIfindex = 62

// MetadataHandler returns Hetzner's metadata service. It answers a server
// by the source address of its request, and in a private network by its
// VRF too; others get 404, as on Hetzner.
func (a *Agent) MetadataHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hetzner/v1/metadata", func(w http.ResponseWriter, r *http.Request) {
		if server := a.caller(r); server != nil {
			writeText(w, server.Metadata)
		} else {
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET /hetzner/v1/metadata/{key}", func(w http.ResponseWriter, r *http.Request) {
		if server := a.caller(r); server == nil {
			http.NotFound(w, r)
		} else if value, found := server.Keys[r.PathValue("key")]; found {
			writeText(w, value)
		} else {
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET /hetzner/v1/userdata", func(w http.ResponseWriter, r *http.Request) {
		switch server := a.caller(r); {
		case server == nil:
			http.NotFound(w, r)
		case server.UserData == "":
			// cloud-init would otherwise wait for content
			w.WriteHeader(http.StatusNoContent)
		default:
			writeText(w, server.UserData)
		}
	})
	return mux
}

// caller returns the server a request comes from, or nil.
func (a *Agent) caller(r *http.Request) *agentproto.MetadataServer {
	source, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return nil
	}
	address := source.Addr().Unmap()
	vrf, _ := r.Context().Value(vrfKey{}).(string)
	metadata := a.metadata.Load()
	for i, server := range metadata.Servers {
		if vrf != "" {
			if slices.Contains(server.Private, agentproto.PrivateSource{VRF: vrf, Address: address}) {
				return &metadata.Servers[i]
			}
			continue
		}
		for _, prefix := range server.Sources {
			if prefix.Contains(address) {
				return &metadata.Servers[i]
			}
		}
	}
	return nil
}

// writeText answers with plain text.
func writeText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// an error means the server went away
	_, _ = io.WriteString(w, text)
}
