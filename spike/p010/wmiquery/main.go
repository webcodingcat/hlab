// Command wmiquery times a Hyper-V state query over WMI (root\virtualization\v2) from Go.
package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/yusufpapurcu/wmi"
)

type msvmComputerSystem struct {
	ElementName  string
	EnabledState uint16
}

func main() {
	query := "SELECT ElementName, EnabledState FROM Msvm_ComputerSystem WHERE ElementName = 'deb-gc'"
	var samples []time.Duration
	for i := 0; i < 21; i++ {
		var result []msvmComputerSystem
		start := time.Now()
		if err := wmi.QueryNamespace(query, &result, `root\virtualization\v2`); err != nil {
			panic(err)
		}
		samples = append(samples, time.Since(start))
		if i == 0 {
			fmt.Printf("first query (incl. COM init): %s, result %+v\n", samples[0], result)
		}
	}
	samples = samples[1:]
	sort.Slice(samples, func(a, b int) bool { return samples[a] < samples[b] })
	fmt.Printf("WMI from Go, state of one VM: median %s, max %s (n=%d)\n", samples[len(samples)/2], samples[len(samples)-1], len(samples))
}
