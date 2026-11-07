// Command hostctl talks to the appliance agent over a Hyper-V socket from Windows.
// Spike for P0-03 only.
//
//	hostctl <vm-name-or-id> ping <count>        round trips over the channel
//	hostctl <vm-name-or-id> wait <seconds>      poll until the agent answers, print the time
//	hostctl <vm-name-or-id> post <path> <file>  send a file to /nft or /ip
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
)

// Linux AF_VSOCK port 5000 maps to this Hyper-V service ID (port in the first field).
const serviceID = "00001388-facb-11e6-bd58-64006a7986d3"

func main() {
	vm, _ := guid.FromString(resolveVM(os.Args[1]))
	service, _ := guid.FromString(serviceID)
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&winio.HvsockDialer{}).Dial(ctx, &winio.HvsockAddr{VMID: vm, ServiceID: service})
			},
		},
	}
	switch os.Args[2] {
	case "ping":
		count, _ := strconv.Atoi(os.Args[3])
		var total, worst time.Duration
		for i := 0; i < count; i++ {
			start := time.Now()
			if err := get(client, "http://agent/ping"); err != nil {
				panic(err)
			}
			took := time.Since(start)
			total += took
			worst = max(worst, took)
		}
		fmt.Printf("ping x%d: avg %s, max %s\n", count, total/time.Duration(count), worst)
	case "wait":
		seconds, _ := strconv.Atoi(os.Args[3])
		start := time.Now()
		for time.Since(start) < time.Duration(seconds)*time.Second {
			if get(client, "http://agent/ping") == nil {
				fmt.Printf("agent answered after %.2f s\n", time.Since(start).Seconds())
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		fmt.Println("agent did not answer")
		os.Exit(1)
	case "post":
		body, _ := os.ReadFile(os.Args[4])
		start := time.Now()
		response, err := client.Post("http://agent"+os.Args[3], "text/plain", strings.NewReader(string(body)))
		if err != nil {
			panic(err)
		}
		answer, _ := io.ReadAll(response.Body)
		fmt.Printf("%s %s(round trip %s)\n", response.Status, answer, time.Since(start))
	}
}

func resolveVM(name string) string {
	if _, err := guid.FromString(name); err == nil {
		return name
	}
	out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", "(Get-VM -Name '"+name+"').Id.Guid").Output()
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(out))
}

func get(client *http.Client, url string) error {
	response, err := client.Get(url)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, response.Body)
	return response.Body.Close()
}
