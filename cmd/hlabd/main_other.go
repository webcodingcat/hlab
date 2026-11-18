//go:build !windows

// Command hlabd is the hlab Windows service.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "hlabd: this version runs on Windows only")
	os.Exit(1)
}
