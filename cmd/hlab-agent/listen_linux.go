//go:build linux

package main

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// listenFreebind listens on the TCP address addr even if the address is not
// (yet) configured on an interface (IP_FREEBIND).
func listenFreebind(addr string) (net.Listener, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_FREEBIND, 1)
		}); err != nil {
			return err
		}
		return serr
	}}
	return lc.Listen(context.Background(), "tcp4", addr)
}
