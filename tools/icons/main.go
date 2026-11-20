// Command icons renders the HLab application icon, the four tray state
// icons, the Windows icon resources of the programs and the SVG artwork
// from one geometry (`task icons`, run from the repository root). Its
// outputs are committed, so `task build` does not run it.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := Generate("."); err != nil {
		fmt.Fprintln(os.Stderr, "icons:", err)
		os.Exit(1)
	}
}
