// Package cidata builds NoCloud seed ISOs (volume label "cidata") that
// configure a guest with cloud-init: meta-data, user-data, vendor-data,
// network-config v2 and optional extra files at the ISO root.
package cidata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

// Seed is everything one guest receives through its seed ISO.
type Seed struct {
	InstanceID string
	Hostname   string
	SSHKeys    []string
	// UserData is passed through as given (a #cloud-config or a script);
	// empty means an empty #cloud-config.
	UserData string
	// VendorData is passed through as given; empty means an empty
	// #cloud-config.
	VendorData string
	// Interfaces are named eth0, eth1, ... in this order.
	Interfaces []Interface
	// Files are extra files at the ISO root, keyed by file name.
	Files map[string][]byte
	// NoOnLink leaves the key on-link out of every route of every
	// interface in network-config, for guests whose renderer is not
	// netplan: they ignore the key and cloud-init's v2 schema rejects it.
	NoOnLink bool
}

// Interface is one network adapter of the guest, matched by MAC address.
type Interface struct {
	MAC       string
	Addresses []netip.Prefix
	// Gateway, when valid, becomes an on-link default route.
	Gateway     netip.Addr
	Routes      []Route
	Nameservers []netip.Addr
}

// Route is a static route of an interface.
type Route struct {
	To  netip.Prefix
	Via netip.Addr
}

const (
	sectorSize  = 2048
	volumeLabel = "cidata"
	emptyConfig = "#cloud-config\n"
)

var seedFiles = []string{"meta-data", "user-data", "vendor-data", "network-config"}

// Build returns the seed ISO image as bytes.
func Build(seed Seed) ([]byte, error) {
	dir, err := os.MkdirTemp("", "hlab-cidata-")
	if err != nil {
		return nil, fmt.Errorf("cidata: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "cidata.iso")
	if err := BuildFile(seed, path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cidata: read image: %w", err)
	}
	return b, nil
}

// BuildFile writes the seed ISO image to path, which must not exist yet.
// On error no file is left at path.
func BuildFile(seed Seed, path string) (err error) {
	contents, err := render(seed)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "hlab-cidata-work-")
	if err != nil {
		return fmt.Errorf("cidata: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	// Room for the volume descriptors, directory records and path tables,
	// plus every file rounded up to whole sectors.
	size := int64(1 << 20)
	for _, data := range contents {
		size += (int64(len(data))/sectorSize + 1) * sectorSize
	}

	d, err := diskfs.Create(path, size, diskfs.SectorSize(sectorSize))
	if err != nil {
		return fmt.Errorf("cidata: create %s: %w", path, err)
	}
	defer func() {
		if cerr := d.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("cidata: close %s: %w", path, cerr)
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()

	fs, err := d.CreateFilesystem(disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: volumeLabel,
		WorkDir:     work,
	})
	if err != nil {
		return fmt.Errorf("cidata: create filesystem: %w", err)
	}
	for name, data := range contents {
		if err := writeFile(fs, "/"+name, data); err != nil {
			return err
		}
	}
	iso, ok := fs.(*iso9660.FileSystem)
	if !ok {
		return errors.New("cidata: unexpected filesystem type")
	}
	if err := iso.Finalize(iso9660.FinalizeOptions{RockRidge: true, VolumeIdentifier: volumeLabel}); err != nil {
		return fmt.Errorf("cidata: finalize: %w", err)
	}
	return nil
}

func writeFile(fs filesystem.FileSystem, name string, data []byte) error {
	f, err := fs.OpenFile(name, os.O_CREATE|os.O_RDWR|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("cidata: open %s: %w", name, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("cidata: write %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("cidata: close %s: %w", name, err)
	}
	return nil
}

// render validates the seed and returns the content of every file of the ISO.
func render(seed Seed) (map[string][]byte, error) {
	if strings.TrimSpace(seed.InstanceID) == "" {
		return nil, errors.New("cidata: instance id is missing")
	}
	if strings.TrimSpace(seed.Hostname) == "" {
		return nil, errors.New("cidata: hostname is missing")
	}

	out := make(map[string][]byte, len(seedFiles)+len(seed.Files))
	for name, data := range seed.Files {
		if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, fmt.Errorf("cidata: invalid file name %q", name)
		}
		for _, reserved := range seedFiles {
			if strings.EqualFold(name, reserved) {
				return nil, fmt.Errorf("cidata: file name %q is reserved", name)
			}
		}
		out[name] = data
	}

	meta, err := marshal(metaData{
		InstanceID:    seed.InstanceID,
		LocalHostname: seed.Hostname,
		PublicKeys:    nonNil(seed.SSHKeys),
	})
	if err != nil {
		return nil, err
	}
	out["meta-data"] = meta

	network, err := networkConfig(seed.Interfaces, !seed.NoOnLink)
	if err != nil {
		return nil, err
	}
	out["network-config"] = network

	out["user-data"] = []byte(orEmptyConfig(seed.UserData))
	out["vendor-data"] = []byte(orEmptyConfig(seed.VendorData))
	return out, nil
}

type metaData struct {
	InstanceID    string   `json:"instance-id"`
	LocalHostname string   `json:"local-hostname"`
	PublicKeys    []string `json:"public-keys"`
}

type network struct {
	Network networkV2 `json:"network"`
}

type networkV2 struct {
	Version   int                 `json:"version"`
	Ethernets map[string]ethernet `json:"ethernets"`
}

type ethernet struct {
	Match       match        `json:"match"`
	SetName     string       `json:"set-name"`
	Addresses   []string     `json:"addresses,omitempty"`
	Optional    bool         `json:"optional,omitempty"`
	Routes      []route      `json:"routes,omitempty"`
	Nameservers *nameservers `json:"nameservers,omitempty"`
}

type match struct {
	MACAddress string `json:"macaddress"`
}

type route struct {
	To     string `json:"to"`
	Via    string `json:"via"`
	OnLink bool   `json:"on-link,omitempty"`
}

type nameservers struct {
	Addresses []string `json:"addresses"`
}

// networkConfig renders network-config version 2. An interface without
// address is optional, so it does not block systemd-networkd-wait-online
// in the guest. Without withOnLink no route carries on-link.
func networkConfig(ifaces []Interface, withOnLink bool) ([]byte, error) {
	cfg := network{Network: networkV2{Version: 2, Ethernets: map[string]ethernet{}}}
	seen := map[string]bool{}
	for i, iface := range ifaces {
		name := fmt.Sprintf("eth%d", i)
		mac, err := normalizeMAC(iface.MAC)
		if err != nil {
			return nil, fmt.Errorf("cidata: interface %s: %w", name, err)
		}
		if seen[mac] {
			return nil, fmt.Errorf("cidata: interface %s: MAC address %s is used twice", name, mac)
		}
		seen[mac] = true
		eth := ethernet{Match: match{MACAddress: mac}, SetName: name, Optional: len(iface.Addresses) == 0}
		for _, p := range iface.Addresses {
			if !p.IsValid() {
				return nil, fmt.Errorf("cidata: interface %s: invalid address", name)
			}
			eth.Addresses = append(eth.Addresses, p.String())
		}
		if iface.Gateway.IsValid() {
			def := netip.PrefixFrom(netip.IPv4Unspecified(), 0)
			if iface.Gateway.Is6() {
				def = netip.PrefixFrom(netip.IPv6Unspecified(), 0)
			}
			eth.Routes = append(eth.Routes, route{To: def.String(), Via: iface.Gateway.String(), OnLink: withOnLink})
		}
		for _, r := range iface.Routes {
			if !r.To.IsValid() || !r.Via.IsValid() {
				return nil, fmt.Errorf("cidata: interface %s: route needs destination and gateway", name)
			}
			eth.Routes = append(eth.Routes, route{
				To:     r.To.Masked().String(),
				Via:    r.Via.String(),
				OnLink: withOnLink && !onLink(iface.Addresses, r.Via),
			})
		}
		if len(iface.Nameservers) > 0 {
			ns := &nameservers{}
			for _, a := range iface.Nameservers {
				if !a.IsValid() {
					return nil, fmt.Errorf("cidata: interface %s: invalid nameserver", name)
				}
				ns.Addresses = append(ns.Addresses, a.String())
			}
			eth.Nameservers = ns
		}
		cfg.Network.Ethernets[name] = eth
	}
	return marshal(cfg)
}

// onLink reports whether via lies inside one of the interface's prefixes;
// a gateway outside them (as with /32 addresses) needs on-link.
func onLink(addrs []netip.Prefix, via netip.Addr) bool {
	for _, p := range addrs {
		if p.Masked().Contains(via) && p.Bits() < via.BitLen() {
			return true
		}
	}
	return false
}

// normalizeMAC accepts the usual notations and Hyper-V's 12 hex digits and
// returns lower-case colon-separated form.
func normalizeMAC(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("MAC address is missing")
	}
	if len(s) == 12 && !strings.ContainsAny(s, ":-.") {
		var b strings.Builder
		for i := 0; i < 12; i += 2 {
			if i > 0 {
				b.WriteByte(':')
			}
			b.WriteString(s[i : i+2])
		}
		s = b.String()
	}
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return "", fmt.Errorf("invalid MAC address %q", s)
	}
	return hw.String(), nil
}

// marshal writes JSON, which cloud-init reads as YAML.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("cidata: encode: %w", err)
	}
	return buf.Bytes(), nil
}

func orEmptyConfig(s string) string {
	if s == "" {
		return emptyConfig
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
