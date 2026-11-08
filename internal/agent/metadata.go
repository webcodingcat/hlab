//go:build linux

package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
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

// MetadataHandler returns Hetzner's metadata service. It answers a server
// by the source address of its request; others get 404, as on Hetzner.
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
	metadata := a.metadata.Load()
	for i, server := range metadata.Servers {
		for _, prefix := range server.Sources {
			if prefix.Contains(source.Addr().Unmap()) {
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
