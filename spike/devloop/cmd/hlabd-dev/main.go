// Command hlabd-dev is a stand-in Windows service for the P0-13 development loop:
// it answers GET /status on the host link with its build version. Spike only.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

type service struct{}

func (service) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	started := time.Now()
	server := &http.Server{Addr: listenAddr(), Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "version=%s up=%s\n", version, time.Since(started).Round(time.Millisecond))
	})}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Print(err)
		}
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for request := range requests {
		if request.Cmd == svc.Stop || request.Cmd == svc.Shutdown {
			break
		}
	}
	status <- svc.Status{State: svc.StopPending}
	server.Close()
	return false, 0
}

func listenAddr() string {
	if addr := os.Getenv("HLAB_DEV_ADDR"); addr != "" {
		return addr
	}
	return "198.18.0.1:8090"
}

func main() {
	inService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatal(err)
	}
	if !inService {
		fmt.Println("version=" + version + " (console)")
		return
	}
	if err := svc.Run("hlabd-dev", service{}); err != nil {
		log.Fatal(err)
	}
}
