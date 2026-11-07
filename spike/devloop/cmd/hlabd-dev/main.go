// Command hlabd-dev stands in for the HLab daemon in the P0-13/P0-09 checks. It runs
// elevated as a normal process (started by the scheduled task "HLab dev"), writes the
// control token for the WSL CLI, and answers on the host link:
//
//	GET  /status         build version and uptime
//	POST /_lab/shutdown  exit (token), so a non-elevated deploy can replace the binary
//	POST /_lab/upload    stream the body to disk and report the rate (token)
//
// Spike only.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

const tokenPath = `C:\ProgramData\HLab\control.token`

func main() {
	token := writeToken()
	started := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "version=%s up=%s\n", version, time.Since(started).Round(time.Millisecond))
	})
	mux.HandleFunc("POST /_lab/shutdown", authorized(token, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "bye")
		go func() { time.Sleep(100 * time.Millisecond); os.Exit(0) }()
	}))
	mux.HandleFunc("POST /_lab/upload", authorized(token, func(w http.ResponseWriter, r *http.Request) {
		file, err := os.Create(`C:\HLabDev\data\upload.bin`)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer file.Close()
		start := time.Now()
		written, err := io.Copy(file, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		seconds := time.Since(start).Seconds()
		fmt.Fprintf(w, "received %d bytes in %.1f s: %.0f MB/s\n", written, seconds, float64(written)/seconds/1e6)
	}))
	addr := os.Getenv("HLAB_DEV_ADDR")
	if addr == "" {
		addr = "198.18.0.1:8090"
	}
	log.Fatal(http.ListenAndServe(addr, mux))
}

// writeToken creates a fresh control token, readable for the WSL CLI through
// /mnt/c (the folder inherits read access for Users from ProgramData).
func writeToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		log.Fatal(err)
	}
	token := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(`C:\HLabDev\data`, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte(token), 0o644); err != nil {
		log.Fatal(err)
	}
	return token
}

func authorized(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
