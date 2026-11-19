package agent

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// HAProxyConfig renders the haproxy.cfg of one load balancer, plus the
// files it references (name -> content, one "<listen_port>-<i>.pem" with
// leaf, chain and key per certificate), with paths below dir (a Linux
// path). Frontends are "fe<listen_port>", backends "be<listen_port>", the
// server names are the target keys, and the port-80 redirect frontend is
// "fe80redirect". An https service without a certificate is left out.
func HAProxyConfig(lb agentapi.LoadBalancer, dir string) (cfg string, files map[string][]byte) {
	files = map[string][]byte{}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("# load balancer %d, written by hlab-agent", lb.ID)
	w("global")
	if lb.MaxConn > 0 {
		w("    maxconn %d", lb.MaxConn)
	}
	w("    stats socket %s mode 600 level operator", path.Join(dir, "haproxy.sock"))
	w("    server-state-file %s", path.Join(dir, "server-state"))
	w("")
	w("defaults")
	w("    load-server-state-from-file global")
	w("    timeout connect 5s")
	w("    timeout client 50s")
	w("    timeout server 50s")

	balance := "roundrobin"
	if lb.Algorithm == "least_connections" {
		balance = "leastconn"
	}
	redirect := false
	for _, s := range lb.Services {
		mode := "tcp"
		var certs []string
		if s.Protocol == "http" || s.Protocol == "https" {
			mode = "http"
		}
		if s.Protocol == "https" {
			if s.HTTP == nil || len(s.HTTP.CertPEM) == 0 {
				continue
			}
			for i, c := range s.HTTP.CertPEM {
				name := fmt.Sprintf("%d-%d.pem", s.ListenPort, i)
				content := c
				if !strings.HasSuffix(content, "\n") {
					content += "\n"
				}
				if i < len(s.HTTP.KeyPEM) {
					content += s.HTTP.KeyPEM[i]
				}
				files[name] = []byte(content)
				certs = append(certs, "crt "+path.Join(dir, name))
			}
			if s.HTTP.RedirectHTTP {
				redirect = true
			}
		}

		w("")
		w("frontend fe%d", s.ListenPort)
		w("    mode %s", mode)
		if len(certs) > 0 {
			w("    bind :%d ssl %s", s.ListenPort, strings.Join(certs, " "))
		} else {
			w("    bind :%d", s.ListenPort)
		}
		if mode == "http" {
			proto := "http"
			if s.Protocol == "https" {
				proto = "https"
			}
			w("    option forwardfor")
			w("    http-request set-header X-Forwarded-Proto %s", proto)
			w("    http-request set-header X-Forwarded-Port %%[dst_port]")
			if s.HTTP != nil && s.HTTP.TimeoutIdle > 0 {
				w("    timeout client %ds", s.HTTP.TimeoutIdle)
			}
		}
		w("    default_backend be%d", s.ListenPort)

		w("")
		w("backend be%d", s.ListenPort)
		w("    mode %s", mode)
		w("    balance %s", balance)
		sticky := mode == "http" && s.HTTP != nil && s.HTTP.StickySessions
		if mode == "http" && s.HTTP != nil && s.HTTP.TimeoutIdle > 0 {
			w("    timeout server %ds", s.HTTP.TimeoutIdle)
		}
		if sticky {
			w("    cookie %s insert indirect nocache maxlife %ds", cookieName(s.HTTP.CookieName), s.HTTP.CookieLifetime)
		}
		hc := s.HealthCheck
		httpCheck := hc.Protocol == "http" || hc.Protocol == "https"
		checkTLS := httpCheck && (hc.TLS || hc.Protocol == "https")
		if httpCheck {
			w("    option httpchk")
			uri := hc.Path
			if uri == "" {
				uri = "/"
			}
			if hc.Domain != "" {
				w("    http-check send meth GET uri %s ver HTTP/1.1 hdr Host %s", arg(uri), arg(hc.Domain))
			} else {
				w("    http-check send meth GET uri %s", arg(uri))
			}
			if len(hc.StatusCodes) > 0 {
				w("    http-check expect rstatus %s", statusRegex(hc.StatusCodes))
			}
			if hc.Response != "" {
				w("    http-check expect string %s", quote(hc.Response))
			}
		}
		if hc.Timeout > 0 {
			w("    timeout check %ds", hc.Timeout)
		}
		for _, t := range lb.Targets {
			line := fmt.Sprintf("    server %s %s:%d", t.Key, t.IP, s.DestinationPort)
			if sticky {
				line += " cookie " + cookieValue(t.Key)
			}
			if s.ProxyProtocol {
				line += " send-proxy-v2 check-send-proxy"
			}
			line += fmt.Sprintf(" check inter %ds fall %d rise %d", hc.Interval, hc.Retries, hc.Retries)
			if hc.Port > 0 {
				line += " port " + strconv.Itoa(hc.Port)
			}
			if checkTLS {
				line += " check-ssl verify none"
			}
			b.WriteString(line + "\n")
		}
	}

	if redirect {
		w("")
		w("frontend fe80redirect")
		w("    mode http")
		w("    bind :80")
		w("    http-request redirect scheme https code 301")
	}
	return b.String(), files
}

// statusRegex turns Hetzner status patterns ("2??", "3*") into one
// anchored regular expression: "?" is one character, "*" any number.
func statusRegex(patterns []string) string {
	parts := make([]string, len(patterns))
	for i, p := range patterns {
		q := regexp.QuoteMeta(strings.Map(dropSpace, p))
		q = strings.ReplaceAll(q, `\?`, ".")
		q = strings.ReplaceAll(q, `\*`, ".*")
		parts[i] = q
	}
	return "^(" + strings.Join(parts, "|") + ")$"
}

// The renderer writes API-supplied strings into haproxy.cfg. Control
// characters (a newline would start a new configuration line) are dropped,
// and every value that is not a plain word is double-quoted with HAProxy's
// escapes, so no value can add or split a configuration line.

// dropControl drops control characters.
func dropControl(r rune) rune {
	if r < 0x20 || r == 0x7f {
		return -1
	}
	return r
}

// dropSpace drops control characters and white space.
func dropSpace(r rune) rune {
	if r <= ' ' || r == 0x7f {
		return -1
	}
	return r
}

// plainArg matches values HAProxy takes unquoted without special meaning.
var plainArg = regexp.MustCompile(`^[A-Za-z0-9._~/:?&=%+@!,;*()\[\]-]+$`)

// arg returns s as one HAProxy argument: a plain word as it is, anything
// else quoted.
func arg(s string) string {
	s = strings.Map(dropControl, s)
	if plainArg.MatchString(s) {
		return s
	}
	return quote(s)
}

// quote writes s as a double-quoted HAProxy argument without control
// characters.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, `#`, `\#`, `'`, `\'`)
	return `"` + r.Replace(strings.Map(dropControl, s)) + `"`
}

// cookieName keeps the characters a cookie name (an RFC 6265 token) may
// hold, so the name is always one plain HAProxy argument.
func cookieName(s string) string {
	return strings.Map(func(r rune) rune {
		if r <= ' ' || r >= 0x7f || strings.ContainsRune(`()<>@,;:\"/[]?={}#$'`, r) {
			return -1
		}
		return r
	}, s)
}

// cookieValue derives the sticky cookie value of a target from its key.
func cookieValue(key string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, key)
}
