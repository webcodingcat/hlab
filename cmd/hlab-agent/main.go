// Command hlab-agent runs inside the hlab appliance VM.
package main

import (
	"fmt"

	"github.com/webcodingcat/hlab/internal/version"
)

func main() {
	fmt.Println("hlab-agent", version.Version)
}
