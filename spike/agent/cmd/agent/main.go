// Command agent runs in the appliance and applies configuration it receives over
// HTTP on AF_VSOCK (hv_sock under Hyper-V). Spike for P0-03 only.
package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"time"

	"github.com/mdlayher/vsock"
)

const port = 5000

func main() {
	listener, err := vsock.Listen(port, nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on vsock port %d", port)
	http.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "pong")
	})
	// nft -f applies a whole file as one transaction: all of it or nothing.
	http.HandleFunc("/nft", runWithBody("nft", "-f", "-"))
	// Addresses and routes; -force keeps going over entries that already exist.
	http.HandleFunc("/ip", runWithBody("ip", "-force", "-batch", "-"))
	log.Fatal(http.Serve(listener, nil))
}

// runWithBody returns a handler that feeds the request body to a command's stdin.
func runWithBody(name string, args ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		start := time.Now()
		cmd := exec.Command(name, args...)
		cmd.Stdin = bytes.NewReader(body)
		output, err := cmd.CombinedOutput()
		if err != nil {
			http.Error(w, fmt.Sprintf("%v: %s", err, output), http.StatusUnprocessableEntity)
			return
		}
		fmt.Fprintf(w, "%s: applied %d bytes in %s\n", name, len(body), time.Since(start))
	}
}
