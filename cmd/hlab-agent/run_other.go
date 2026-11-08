//go:build !linux

package main

import "errors"

// run refuses: the agent needs netlink and vsock of Linux. `bin/dev build`
// builds it only for Linux; this file keeps the package building elsewhere.
func run() error {
	return errors.New("runs on Linux only, inside the appliance")
}
