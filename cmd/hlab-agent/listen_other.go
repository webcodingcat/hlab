//go:build !linux

package main

import (
	"errors"
	"net"
)

// listenFreebind needs Linux; hlab-agent runs only in the appliance.
func listenFreebind(string) (net.Listener, error) {
	return nil, errors.New("binding an unconfigured address needs Linux")
}
