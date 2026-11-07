// Command hlab is the HLab command line for users.
//
// For now it only reports its version; the parts follow phase by phase
// (docs/roadmap.md).
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
		fmt.Println("hlab", version)
		return
	}
	fmt.Fprintln(os.Stderr, "hlab: not yet implemented; try --version")
	os.Exit(2)
}
