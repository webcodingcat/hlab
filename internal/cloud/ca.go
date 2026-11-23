//go:build windows

package cloud

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/ca"
	"github.com/webcodingcat/hlab/internal/hostnet"
	"github.com/webcodingcat/hlab/internal/scheduler"
)

const (
	// crlPath is where hlabd serves the CRL on its API listener, outside
	// the token-checked /v1 tree (PLAN 08 D6; ca.CRLURL).
	crlPath = "/hlab/v1/ca.crl"
	// crlRenewEvery renews the CRL well before its nextUpdate.
	crlRenewEvery = 7 * 24 * time.Hour
	// problemTrustCA is the /status problem of a failed trust at start.
	problemTrustCA = "trust_ca"
)

// openCA opens (or creates) the HLab CA of the profile, renews its CRL and,
// when trust_ca is on, adds it to the host's trust stores. A failed trust
// is a problem, not a reason to stop the start.
func (c *Cloud) openCA(ctx context.Context) error {
	var err error
	if c.ca, err = ca.Open(c.d.Paths.CA, caName(c.tag), nil); err != nil {
		return err
	}
	if err := c.ca.RenewCRL(); err != nil {
		return err
	}
	if c.set.Certificates.TrustCA {
		if err := c.net.TrustCA(ctx, c.ca.Certificate(), true); err != nil {
			slog.Warn("trust the HLab CA", "err", err)
			c.problems = append(c.problems, hostnet.Problem{Code: problemTrustCA, Message: err.Error()})
		}
	}
	return nil
}

// caName is the common name of a new CA of the profile with tag: "HLab CA"
// for the release, "HLab-dev CA" for the dev profile. An existing CA keeps
// the name it was created with.
func caName(tag string) string {
	return "HLab" + strings.TrimPrefix(tag, "hlab") + " CA"
}

// trust adds the CA to the host's trust stores (on) or removes it, and
// keeps the setting of the running cloud in step.
func (c *Cloud) trust(ctx context.Context, on bool) error {
	if err := c.net.TrustCA(ctx, c.ca.Certificate(), on); err != nil {
		return err
	}
	c.set.Certificates.TrustCA = on
	return nil
}

// withCRL serves the CRL at crlPath without a token and everything else
// through api.
func (c *Cloud) withCRL(api http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+crlPath, func(w http.ResponseWriter, _ *http.Request) {
		der, err := c.ca.CRL()
		if err != nil {
			slog.Error("serve the CRL", "err", err)
			http.Error(w, "CRL unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pkix-crl")
		_, _ = w.Write(der)
	})
	mux.Handle("/", api)
	return mux
}

// cloudJobs are the jobs of the cloud itself.
func (c *Cloud) cloudJobs() []scheduler.Job {
	return []scheduler.Job{{
		Name:  "crl",
		Every: crlRenewEvery,
		Run: func(context.Context) {
			if err := c.ca.RenewCRL(); err != nil {
				slog.Error("renew the CRL", "err", err)
			}
		},
	}, {
		Name:  "storage-tls",
		Every: storageTLSCheckEvery,
		Run:   c.renewStorageTLS,
	}}
}
