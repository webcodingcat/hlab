// Command hlab is the HLab command line for users: it controls the running
// hlabd through its lab control.
package main

import (
	"os"

	"github.com/webcodingcat/hlab/internal/cli"
)

// version is set at build time by bin/dev build (-ldflags -X main.version=...).
var version = "dev"

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr, version))
}
