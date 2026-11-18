// Command hlab is the management command of hlab.
package main

import (
	"fmt"

	"github.com/webcodingcat/hlab/internal/version"
)

func main() {
	fmt.Println("hlab", version.Version)
}
