// Command hlabd is the hlab Windows service.
package main

import (
	"fmt"

	"github.com/webcodingcat/hlab/internal/version"
)

func main() {
	fmt.Println("hlabd", version.Version)
}
