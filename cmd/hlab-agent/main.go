// Command hlab-agent is the agent inside the HLab appliance: it applies the
// pictures hlabd sends over vsock and runs the metadata service
// (internal/agent). It runs on Linux only.
package main

import (
	"flag"
	"fmt"
	"os"
)

// version is set at build time by bin/dev build (-ldflags -X main.version=...).
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("hlab-agent", version)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hlab-agent:", err)
		os.Exit(1)
	}
}
