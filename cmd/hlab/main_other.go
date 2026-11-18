//go:build !windows

// Command hlab is the management command of hlab.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "hlab: this version runs on Windows only")
	os.Exit(1)
}
