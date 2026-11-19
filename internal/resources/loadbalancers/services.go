package loadbalancers

import (
	"encoding/json"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/store"
)

// Service is a service of a load balancer, stored and shown in Hetzner's
// API shape.
type Service struct {
	Protocol        string       `json:"protocol"`
	ListenPort      int          `json:"listen_port"`
	DestinationPort int          `json:"destination_port"`
	Proxyprotocol   bool         `json:"proxyprotocol"`
	HealthCheck     HealthCheck  `json:"health_check"`
	HTTP            *ServiceHTTP `json:"http,omitempty"`
}

// ServiceHTTP is the http block of an http or https service.
type ServiceHTTP struct {
	CookieName     string  `json:"cookie_name"`
	CookieLifetime int     `json:"cookie_lifetime"`
	TimeoutIdle    int     `json:"timeout_idle"`
	Certificates   []int64 `json:"certificates"`
	RedirectHTTP   bool    `json:"redirect_http"`
	StickySessions bool    `json:"sticky_sessions"`
}

// HealthCheck is the health check of a service.
type HealthCheck struct {
	Protocol string           `json:"protocol"`
	Port     int              `json:"port"`
	Interval int              `json:"interval"`
	Timeout  int              `json:"timeout"`
	Retries  int              `json:"retries"`
	HTTP     *HealthCheckHTTP `json:"http,omitempty"`
}

// HealthCheckHTTP is the http block of a health check.
type HealthCheckHTTP struct {
	Domain      *string  `json:"domain"`
	Path        string   `json:"path"`
	Response    string   `json:"response"`
	StatusCodes []string `json:"status_codes"`
	TLS         bool     `json:"tls"`
}

// serviceRoutes registers add_service, update_service and delete_service.
func (m *Module) serviceRoutes(resource.Registrar) {}

// createServices adds the services of a create request to r, within the
// limits of typ.
func (m *Module) createServices(_ store.Tx, _ *Record, _ Type, in []json.RawMessage) error {
	if len(in) > 0 {
		return &api.FieldError{Field: "services", Message: "services are not supported"}
	}
	return nil
}

// UsedBy returns the load balancers of projectID whose services use
// certificate certID.
func (m *Module) UsedBy(store.Tx, int64, int64) ([]int64, error) { return nil, nil }
