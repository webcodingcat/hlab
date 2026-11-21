package loadbalancers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/certificates"
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

// Service protocols and defaults (RESEARCH 3.1).
const (
	protoTCP   = "tcp"
	protoHTTP  = "http"
	protoHTTPS = "https"

	defaultCookieName     = "HCLBSTICKY"
	defaultCookieLifetime = 300
	defaultTimeoutIdle    = 50
	// redirectPort is the port redirect_http listens on.
	redirectPort = 80
)

var defaultStatusCodes = []string{"2??", "3??"}

// serviceInput is a service in add_service, update_service and the
// services of a create request; nil fields are not given.
type serviceInput struct {
	Protocol        *string           `json:"protocol"`
	ListenPort      *int              `json:"listen_port"`
	DestinationPort *int              `json:"destination_port"`
	Proxyprotocol   *bool             `json:"proxyprotocol"`
	HealthCheck     *healthCheckInput `json:"health_check"`
	HTTP            *struct {
		CookieName     *string  `json:"cookie_name"`
		CookieLifetime *int     `json:"cookie_lifetime"`
		TimeoutIdle    *int     `json:"timeout_idle"`
		Certificates   *[]int64 `json:"certificates"`
		RedirectHTTP   *bool    `json:"redirect_http"`
		StickySessions *bool    `json:"sticky_sessions"`
	} `json:"http"`
}

type healthCheckInput struct {
	Protocol *string `json:"protocol"`
	Port     *int    `json:"port"`
	Interval *int    `json:"interval"`
	Timeout  *int    `json:"timeout"`
	Retries  *int    `json:"retries"`
	HTTP     *struct {
		Domain      *string   `json:"domain"`
		Path        *string   `json:"path"`
		Response    *string   `json:"response"`
		StatusCodes *[]string `json:"status_codes"`
		TLS         *bool     `json:"tls"`
	} `json:"http"`
}

// serviceRoutes registers add_service, update_service and delete_service.
func (m *Module) serviceRoutes(r resource.Registrar) {
	r.Handle("POST /load_balancers/{id}/actions/add_service", http.HandlerFunc(m.addService))
	r.Handle("POST /load_balancers/{id}/actions/update_service", http.HandlerFunc(m.updateService))
	r.Handle("POST /load_balancers/{id}/actions/delete_service", http.HandlerFunc(m.deleteService))
}

func (m *Module) addService(w http.ResponseWriter, req *http.Request) {
	var in serviceInput
	id, ok := decodeAction(w, req, &in)
	if !ok {
		return
	}
	m.queued(w, req, id, "add_service", false, func(tx store.Tx, r *Record) ([]actions.Ref, error) {
		typ, _ := m.lbType(r.Hlab.Type)
		return nil, m.addTo(tx, r, typ, in)
	})
}

// updateService changes the service on listen_port with the fields given;
// a taken port answers 422 source_port_already_used (D8).
func (m *Module) updateService(w http.ResponseWriter, req *http.Request) {
	var in serviceInput
	id, ok := decodeAction(w, req, &in)
	if !ok {
		return
	}
	m.queued(w, req, id, "update_service", false, func(tx store.Tx, r *Record) ([]actions.Ref, error) {
		i, err := findService(*r, in.ListenPort)
		if err != nil {
			return nil, err
		}
		s, err := applyService(r.Services[i], in, false)
		if err != nil {
			return nil, err
		}
		typ, _ := m.lbType(r.Hlab.Type)
		return nil, m.place(tx, r, typ, i, s, http.StatusUnprocessableEntity)
	})
}

func (m *Module) deleteService(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ListenPort *int `json:"listen_port"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.queued(w, req, id, "delete_service", false, func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		i, err := findService(*r, body.ListenPort)
		if err != nil {
			return nil, err
		}
		r.Services = slices.Delete(r.Services, i, i+1)
		return nil, nil
	})
}

// findService returns the index of the service of r on port; a missing
// port or service answers invalid_input.
func findService(r Record, port *int) (int, error) {
	if port == nil {
		return 0, &api.FieldError{Field: "listen_port", Message: "listen_port is required"}
	}
	i := slices.IndexFunc(r.Services, func(s Service) bool { return s.ListenPort == *port })
	if i < 0 {
		return 0, &api.FieldError{Field: "listen_port", Message: fmt.Sprintf("no service on port %d", *port)}
	}
	return i, nil
}

// createServices adds the services of a create request to r, within the
// limits of typ.
func (m *Module) createServices(tx store.Tx, r *Record, typ Type, in []json.RawMessage) error {
	for _, raw := range in {
		var s serviceInput
		if err := json.Unmarshal(raw, &s); err != nil {
			return &api.FieldError{Field: "services", Message: "invalid service: " + err.Error()}
		}
		if err := m.addTo(tx, r, typ, s); err != nil {
			return err
		}
	}
	return nil
}

// addTo adds the service in to r; a taken port answers 412
// source_port_already_used.
func (m *Module) addTo(tx store.Tx, r *Record, typ Type, in serviceInput) error {
	s, err := applyService(Service{}, in, true)
	if err != nil {
		return err
	}
	return m.place(tx, r, typ, -1, s, 0)
}

// place checks s and stores it in r at index i (-1: appended). A port
// another service takes answers source_port_already_used with portStatus
// (0: the status of its table). The type's limits of services and
// certificates answer 403 resource_limit_exceeded.
func (m *Module) place(tx store.Tx, r *Record, typ Type, i int, s Service, portStatus int) error {
	if err := checkService(s); err != nil {
		return err
	}
	if s.HTTP != nil {
		for _, c := range s.HTTP.Certificates {
			_, _, err := certificates.KeyPair(tx, r.Hlab.ProjectID, c)
			if errors.Is(err, store.ErrNotFound) {
				return &api.FieldError{Field: "certificates", Message: fmt.Sprintf("certificate %d not found", c)}
			}
			if err != nil && !errors.Is(err, certificates.ErrNotIssued) {
				return err
			}
		}
	}
	next := slices.Clone(r.Services)
	others := next
	if i >= 0 {
		others = slices.Delete(slices.Clone(r.Services), i, i+1)
		next[i] = s
	} else {
		next = append(next, s)
	}
	if err := checkPorts(others, s, portStatus); err != nil {
		return err
	}
	if len(next) > typ.MaxServices {
		return &api.FieldError{Code: "resource_limit_exceeded",
			Message: fmt.Sprintf("the load balancer type allows %d services", typ.MaxServices)}
	}
	if countCertificates(next) > typ.MaxAssignedCertificates {
		return &api.FieldError{Code: "resource_limit_exceeded",
			Message: fmt.Sprintf("the load balancer type allows %d certificates", typ.MaxAssignedCertificates)}
	}
	r.Services = next
	return nil
}

// checkPorts refuses s when its listen port, or port 80 for its HTTP
// redirect, is taken by one of others or their redirects.
func checkPorts(others []Service, s Service, status int) error {
	taken := map[int]bool{}
	for _, o := range others {
		taken[o.ListenPort] = true
		if o.HTTP != nil && o.HTTP.RedirectHTTP {
			taken[redirectPort] = true
		}
	}
	port := 0
	switch {
	case taken[s.ListenPort]:
		port = s.ListenPort
	case s.HTTP != nil && s.HTTP.RedirectHTTP && (taken[redirectPort] || s.ListenPort == redirectPort):
		port = redirectPort
	default:
		return nil
	}
	return &api.FieldError{Code: "source_port_already_used", Field: "listen_port",
		Message: fmt.Sprintf("port %d is already used by another service", port), Status: status}
}

// countCertificates is the number of distinct certificates of services.
func countCertificates(services []Service) int {
	certs := map[int64]bool{}
	for _, s := range services {
		if s.HTTP != nil {
			for _, c := range s.HTTP.Certificates {
				certs[c] = true
			}
		}
	}
	return len(certs)
}

// applyService returns s changed by the fields of in. For a new service
// (add) http and https default to listen port 80 and 443 and destination
// port 80, and an omitted health check is tcp on the destination port
// with interval 15, timeout 10 and retries 3. A cookie_lifetime or
// timeout_idle of 0 and an empty cookie_name keep the stored value.
func applyService(s Service, in serviceInput, add bool) (Service, error) {
	if in.Protocol != nil {
		s.Protocol = *in.Protocol
	}
	if s.Protocol != protoTCP && s.Protocol != protoHTTP && s.Protocol != protoHTTPS {
		return s, &api.FieldError{Field: "protocol", Message: "protocol must be tcp, http or https"}
	}
	setInt(&s.ListenPort, in.ListenPort)
	setInt(&s.DestinationPort, in.DestinationPort)
	if in.Proxyprotocol != nil {
		s.Proxyprotocol = *in.Proxyprotocol
	}
	if add && s.Protocol != protoTCP {
		if s.ListenPort == 0 {
			s.ListenPort = 80
			if s.Protocol == protoHTTPS {
				s.ListenPort = 443
			}
		}
		if s.DestinationPort == 0 {
			s.DestinationPort = 80
		}
	}

	if s.Protocol == protoTCP {
		if in.HTTP != nil {
			return s, &api.FieldError{Field: "http", Message: "http is only available for http and https services"}
		}
		s.HTTP = nil
	} else {
		h := ServiceHTTP{CookieName: defaultCookieName, CookieLifetime: defaultCookieLifetime, TimeoutIdle: defaultTimeoutIdle}
		if s.HTTP != nil {
			h = *s.HTTP
		}
		if i := in.HTTP; i != nil {
			if i.CookieName != nil && *i.CookieName != "" {
				h.CookieName = *i.CookieName
			}
			if i.CookieLifetime != nil && *i.CookieLifetime != 0 {
				h.CookieLifetime = *i.CookieLifetime
			}
			if i.TimeoutIdle != nil && *i.TimeoutIdle != 0 {
				h.TimeoutIdle = *i.TimeoutIdle
			}
			if i.Certificates != nil {
				h.Certificates = *i.Certificates
			}
			if i.RedirectHTTP != nil {
				h.RedirectHTTP = *i.RedirectHTTP
			}
			if i.StickySessions != nil {
				h.StickySessions = *i.StickySessions
			}
		}
		h.Certificates = slices.Clone(h.Certificates)
		if h.Certificates == nil {
			h.Certificates = []int64{}
		}
		s.HTTP = &h
	}

	if add {
		s.HealthCheck = HealthCheck{Protocol: protoTCP, Port: s.DestinationPort, Interval: 15, Timeout: 10, Retries: 3}
	}
	if in.HealthCheck != nil {
		s.HealthCheck = applyHealthCheck(s.HealthCheck, *in.HealthCheck)
	}
	return s, nil
}

// applyHealthCheck returns c changed by the fields of in. Protocol https
// is stored as http with tls, the form the API shows.
func applyHealthCheck(c HealthCheck, in healthCheckInput) HealthCheck {
	tls := false
	if in.Protocol != nil {
		c.Protocol = *in.Protocol
		if c.Protocol == protoHTTPS {
			c.Protocol, tls = protoHTTP, true
		}
	}
	setInt(&c.Port, in.Port)
	setInt(&c.Interval, in.Interval)
	setInt(&c.Timeout, in.Timeout)
	setInt(&c.Retries, in.Retries)
	if c.Protocol != protoHTTP {
		c.HTTP = nil
		return c
	}
	h := HealthCheckHTTP{Path: "/"}
	if c.HTTP != nil {
		h = *c.HTTP
	}
	if i := in.HTTP; i != nil {
		if i.Domain != nil {
			h.Domain = i.Domain
		}
		if i.Path != nil {
			h.Path = *i.Path
		}
		if i.Response != nil {
			h.Response = *i.Response
		}
		if i.StatusCodes != nil {
			h.StatusCodes = *i.StatusCodes
		}
		if i.TLS != nil {
			h.TLS = *i.TLS
		}
	}
	if tls {
		h.TLS = true
	}
	if len(h.StatusCodes) == 0 {
		h.StatusCodes = slices.Clone(defaultStatusCodes)
	}
	c.HTTP = &h
	return c
}

func setInt(dst, v *int) {
	if v != nil {
		*dst = *v
	}
}

// checkService validates the ports, the http block and the health check
// of s.
func checkService(s Service) error {
	if err := checkPort("listen_port", s.ListenPort); err != nil {
		return err
	}
	if err := checkPort("destination_port", s.DestinationPort); err != nil {
		return err
	}
	if h := s.HTTP; h != nil {
		switch {
		case h.CookieName == "" || utf8.RuneCountInString(h.CookieName) > 100 || hasControl(h.CookieName):
			return &api.FieldError{Field: "cookie_name", Message: "cookie_name must have 1 to 100 characters and no control characters"}
		case h.CookieLifetime < 30 || h.CookieLifetime > 86400:
			return &api.FieldError{Field: "cookie_lifetime", Message: "cookie_lifetime must be between 30 and 86400"}
		case h.TimeoutIdle < 30 || h.TimeoutIdle > 300:
			return &api.FieldError{Field: "timeout_idle", Message: "timeout_idle must be between 30 and 300"}
		case h.RedirectHTTP && s.Protocol != protoHTTPS:
			return &api.FieldError{Field: "redirect_http", Message: "redirect_http is only available for https services"}
		case s.Protocol == protoHTTP && len(h.Certificates) > 0:
			return &api.FieldError{Field: "certificates", Message: "certificates are only available for https services"}
		case s.Protocol == protoHTTPS && len(h.Certificates) == 0:
			return &api.FieldError{Field: "certificates", Message: "an https service needs at least one certificate"}
		}
	}
	return checkHealthCheck(s.HealthCheck)
}

func checkHealthCheck(c HealthCheck) error {
	switch {
	case c.Protocol != protoTCP && c.Protocol != protoHTTP:
		return &api.FieldError{Field: "health_check", Message: "health check protocol must be tcp, http or https"}
	case c.Interval < 3 || c.Interval > 60:
		return &api.FieldError{Field: "interval", Message: "interval must be between 3 and 60"}
	case c.Timeout < 1 || c.Timeout > 60:
		return &api.FieldError{Field: "timeout", Message: "timeout must be between 1 and 60"}
	case c.Retries < 1 || c.Retries > 5:
		return &api.FieldError{Field: "retries", Message: "retries must be between 1 and 5"}
	}
	if err := checkPort("port", c.Port); err != nil {
		return err
	}
	h := c.HTTP
	if h == nil {
		return nil
	}
	if h.Domain != nil && (len(*h.Domain) > 128 || strings.ContainsAny(*h.Domain, " %\\") || hasControl(*h.Domain)) {
		return &api.FieldError{Field: "domain", Message: "domain must have at most 128 characters and no spaces, percent, backslash or control characters"}
	}
	if h.Path == "" || len(h.Path) > 256 || strings.Contains(h.Path, " ") || hasControl(h.Path) {
		return &api.FieldError{Field: "path", Message: "path must have 1 to 256 characters and no spaces or control characters"}
	}
	if len(h.Response) > 256 || hasControl(h.Response) {
		return &api.FieldError{Field: "response", Message: "response must have at most 256 characters and no control characters"}
	}
	if len(h.StatusCodes) > 20 {
		return &api.FieldError{Field: "status_codes", Message: "at most 20 status codes"}
	}
	for _, sc := range h.StatusCodes {
		if sc == "" || strings.Trim(sc, "0123456789?*") != "" {
			return &api.FieldError{Field: "status_codes", Message: "status codes take digits and the wildcards ? and *"}
		}
	}
	return nil
}

func checkPort(field string, p int) error {
	if p < 1 || p > 65535 {
		return &api.FieldError{Field: field, Message: field + " must be between 1 and 65535"}
	}
	return nil
}

// hasControl reports whether s has a control character; the agent's
// renderer would only drop them from the HAProxy configuration.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

// UsedBy returns the load balancers of projectID whose services use
// certificate certID.
func (m *Module) UsedBy(tx store.Tx, projectID, certID int64) ([]int64, error) {
	recs, err := store.List[Record](tx, Kind, projectID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range recs {
		if slices.ContainsFunc(r.Services, func(s Service) bool {
			return s.HTTP != nil && slices.Contains(s.HTTP.Certificates, certID)
		}) {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}
