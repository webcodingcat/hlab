package loadbalancers

import (
	"errors"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/resources/certificates"
	"github.com/webcodingcat/hlab/internal/store"
)

// Contribute implements resource.NetContributor: one agentapi.LoadBalancer
// per load balancer, and the PTR name of each load balancer's IPv4.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if p := r.Hlab.Pending; p != nil {
			a, err := store.GetAction(tx, p.Action)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err == nil && a.Status == actions.StatusRunning {
				r = p.apply(r)
			}
		}
		l, err := m.document(tx, r)
		if err != nil {
			return err
		}
		doc.LoadBalancers = append(doc.LoadBalancers, l)
		if a := r.IPv4(); a.IsValid() {
			if doc.PTR == nil {
				doc.PTR = map[netip.Addr]string{}
			}
			doc.PTR[a] = rdns.PTR(r.DNSPtr, a)
		}
	}
	return nil
}

// document is load balancer r as the agent runs it.
func (m *Module) document(tx store.Tx, r Record) (agentapi.LoadBalancer, error) {
	l := agentapi.LoadBalancer{ID: r.ID, Algorithm: r.Algorithm}
	if r.PublicInterface {
		l.PublicIPv4 = r.IPv4()
	}
	for _, p := range r.PrivateNet {
		if a, err := netip.ParseAddr(p.IP); err == nil {
			l.Private = append(l.Private, agentapi.LBNetwork{NetworkID: p.Network, VLAN: r.Hlab.VLAN, IP: a})
		}
	}
	if t, ok := m.lbType(r.Hlab.Type); ok {
		l.MaxConn = t.MaxConnections
	}
	for _, s := range r.Services {
		svc, err := service(tx, r.Hlab.ProjectID, s)
		if err != nil {
			return l, err
		}
		l.Services = append(l.Services, svc)
	}
	ts, err := m.resolve(tx, r)
	if err != nil {
		return l, err
	}
	for _, t := range ts {
		l.Targets = append(l.Targets, agentapi.LBTarget{Key: t.Key, IP: t.IP, ViaNetworkID: t.ViaNetworkID})
	}
	return l, nil
}

// service is service s of a load balancer of projectID with its
// certificates' PEMs; a managed certificate not yet issued is left out.
func service(tx store.Tx, projectID int64, s Service) (agentapi.LBService, error) {
	out := agentapi.LBService{
		Protocol:        s.Protocol,
		ListenPort:      s.ListenPort,
		DestinationPort: s.DestinationPort,
		ProxyProtocol:   s.Proxyprotocol,
		HealthCheck: agentapi.LBHealthCheck{
			Protocol: s.HealthCheck.Protocol,
			Port:     s.HealthCheck.Port,
			Interval: s.HealthCheck.Interval,
			Timeout:  s.HealthCheck.Timeout,
			Retries:  s.HealthCheck.Retries,
		},
	}
	if h := s.HealthCheck.HTTP; h != nil {
		out.HealthCheck.Path = h.Path
		out.HealthCheck.Response = h.Response
		out.HealthCheck.StatusCodes = h.StatusCodes
		out.HealthCheck.TLS = h.TLS
		if h.Domain != nil {
			out.HealthCheck.Domain = *h.Domain
		}
	}
	if h := s.HTTP; h != nil {
		out.HTTP = &agentapi.LBHTTP{
			CookieName:     h.CookieName,
			CookieLifetime: h.CookieLifetime,
			StickySessions: h.StickySessions,
			RedirectHTTP:   h.RedirectHTTP,
			TimeoutIdle:    h.TimeoutIdle,
		}
		for _, id := range h.Certificates {
			c, k, err := certificates.KeyPair(tx, projectID, id)
			if errors.Is(err, certificates.ErrNotIssued) {
				continue
			}
			if err != nil {
				return out, err
			}
			out.HTTP.CertPEM = append(out.HTTP.CertPEM, c)
			out.HTTP.KeyPEM = append(out.HTTP.KeyPEM, k)
		}
	}
	return out, nil
}
