// Command hlab-tray is the tray program of hlab.
package main

import (
	"fmt"

	"github.com/webcodingcat/hlab/internal/version"
)

func main() {
	fmt.Println("hlab-tray", version.Version)
}
