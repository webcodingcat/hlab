// Package webui is the lab's web UI: its page, built in through go:embed,
// and the checks that keep other websites out of the lab control's port.
//
// The page has no login and keeps no token: it gets a cookie with the page,
// which the lab control takes instead of the control token (Admits). The
// Host check stops DNS rebinding (a foreign name that resolves to the lab);
// the Origin check stops other websites from sending changes (CSRF); the
// cookie is SameSite=Strict, so other websites' requests do not carry it.
package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net"
	"net/http"

	"github.com/webcodingcat/hlab/internal/web"
)

//go:embed files
var files embed.FS

// CookieName is the name of the cookie that admits the page to the lab
// control.
const CookieName = "hlab_ui"

// UI is the web UI of one running lab.
type UI struct {
	hosts  map[string]bool // the Host headers the port answers to
	cookie string          // the cookie's value, new at each start
	logger *slog.Logger
}

// New returns the web UI for the addresses the port listens on, each with
// its port ("127.0.0.1:8090"). A loopback address also takes "localhost".
func New(addresses []string, logger *slog.Logger) *UI {
	if logger == nil {
		logger = slog.Default()
	}
	hosts := map[string]bool{}
	for _, address := range addresses {
		hosts[address] = true
		if host, port, err := net.SplitHostPort(address); err == nil {
			if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
				hosts[net.JoinHostPort("localhost", port)] = true
			}
		}
	}
	random := make([]byte, 32)
	_, _ = rand.Read(random) // never fails, as its documentation says
	return &UI{hosts: hosts, cookie: hex.EncodeToString(random), logger: logger}
}

// Guard checks every request to the port before next gets it: the Host must
// be one of the lab's addresses, and a change (anything but GET, HEAD and
// OPTIONS) must come from the lab's own page (Origin) or carry a token,
// which browsers never add on their own.
func (ui *UI) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ui.hosts[r.Host] {
			web.WriteError(w, ui.logger, forbidden("unknown host "+r.Host+": use the lab's address"))
			return
		}
		if !safe(r.Method) {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != "http://"+r.Host {
				web.WriteError(w, ui.logger, forbidden("requests from "+origin+" may not change the lab"))
				return
			}
			if origin == "" && r.Header.Get("Authorization") == "" {
				web.WriteError(w, ui.logger, forbidden("a change needs an Origin header or a token"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// safe tells whether a method only reads.
func safe(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// forbidden returns the error the Guard answers with.
func forbidden(message string) *web.Error {
	return &web.Error{Status: http.StatusForbidden, Code: "forbidden", Message: message}
}

// Handler serves the page's files; the page itself comes with the cookie.
func (ui *UI) Handler() http.Handler {
	root, _ := fs.Sub(files, "files") // the directory is built in
	fileServer := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{
				Name:     CookieName,
				Value:    ui.cookie,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		fileServer.ServeHTTP(w, r)
	})
}

// Admits tells whether a request comes from the page: it carries the cookie.
func (ui *UI) Admits(r *http.Request) bool {
	cookie, err := r.Cookie(CookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(ui.cookie)) == 1
}
