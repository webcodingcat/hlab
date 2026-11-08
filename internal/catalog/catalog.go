// Package catalog holds what Hetzner offers and the lab answers from a file:
// locations, server, load balancer and Storage Box types, prices, ISOs and
// system images, each with Hetzner's real ID and name, because the tools
// check them. The built-in catalog is catalog.yaml in this folder; a user's
// catalog.yaml in the data folder changes it.
package catalog

import (
	"cmp"
	_ "embed" // the built-in catalog
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"go.yaml.in/yaml/v4"
)

//go:embed catalog.yaml
var builtin []byte

// Catalog is everything the lab offers. Its field names are those of
// Hetzner's API, so that an answer of the real API reads as a catalog
// (DEVELOPING.md, Recipes).
type Catalog struct {
	Locations         []Location         `yaml:"locations"`
	ServerTypes       []ServerType       `yaml:"server_types"`
	LoadBalancerTypes []LoadBalancerType `yaml:"load_balancer_types"`
	StorageBoxTypes   []StorageBoxType   `yaml:"storage_box_types"`
	Images            []Image            `yaml:"images"`
	ISOs              []ISO              `yaml:"isos"`
	Pricing           Pricing            `yaml:"pricing"`
}

// Location is one of Hetzner's locations (fsn1).
type Location struct {
	ID          int64   `yaml:"id"`
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Country     string  `yaml:"country"`
	City        string  `yaml:"city"`
	Latitude    float64 `yaml:"latitude"`
	Longitude   float64 `yaml:"longitude"`
	NetworkZone string  `yaml:"network_zone"`
}

// Deprecation tells when Hetzner announced that something goes away and
// from when it is no longer available.
type Deprecation struct {
	Announced        time.Time `yaml:"announced"`
	UnavailableAfter time.Time `yaml:"unavailable_after"`
}

// Price is an amount without and with VAT, as Hetzner writes it: a decimal
// in a string, which the lab sends on unchanged.
type Price struct {
	Net   string `yaml:"net"`
	Gross string `yaml:"gross"`
}

// TypePrice is the price of a server or load balancer type in a location.
type TypePrice struct {
	Location          string `yaml:"location"`
	PriceHourly       Price  `yaml:"price_hourly"`
	PriceMonthly      Price  `yaml:"price_monthly"`
	IncludedTraffic   uint64 `yaml:"included_traffic"` // bytes
	PricePerTBTraffic Price  `yaml:"price_per_tb_traffic"`
}

// TypeLocation is a location where a server type is offered.
type TypeLocation struct {
	Name        string       `yaml:"name"`
	Available   bool         `yaml:"available"`
	Recommended bool         `yaml:"recommended"`
	Deprecation *Deprecation `yaml:"deprecation"`
}

// ServerType is a server type (cpx22). The lab has x86 types only: Hyper-V
// on an x86 host runs no ARM guests.
type ServerType struct {
	ID           int64          `yaml:"id"`
	Name         string         `yaml:"name"`
	Description  string         `yaml:"description"`
	Category     string         `yaml:"category"`
	Cores        int            `yaml:"cores"`
	Memory       float64        `yaml:"memory"` // GB
	Disk         int            `yaml:"disk"`   // GB
	StorageType  string         `yaml:"storage_type"`
	CPUType      string         `yaml:"cpu_type"`
	Architecture string         `yaml:"architecture"`
	Deprecation  *Deprecation   `yaml:"deprecation"`
	Locations    []TypeLocation `yaml:"locations"`
	Prices       []TypePrice    `yaml:"prices"`
}

// LoadBalancerType is a load balancer type (lb11).
type LoadBalancerType struct {
	ID                      int64        `yaml:"id"`
	Name                    string       `yaml:"name"`
	Description             string       `yaml:"description"`
	MaxConnections          int          `yaml:"max_connections"`
	MaxServices             int          `yaml:"max_services"`
	MaxTargets              int          `yaml:"max_targets"`
	MaxAssignedCertificates int          `yaml:"max_assigned_certificates"`
	Deprecation             *Deprecation `yaml:"deprecation"`
	Prices                  []TypePrice  `yaml:"prices"`
}

// BoxPrice is the price of a Storage Box type in a location.
type BoxPrice struct {
	Location     string `yaml:"location"`
	PriceHourly  Price  `yaml:"price_hourly"`
	PriceMonthly Price  `yaml:"price_monthly"`
	SetupFee     Price  `yaml:"setup_fee"`
}

// StorageBoxType is a Storage Box type (bx11).
type StorageBoxType struct {
	ID                     int64        `yaml:"id"`
	Name                   string       `yaml:"name"`
	Description            string       `yaml:"description"`
	Size                   int64        `yaml:"size"` // bytes
	SnapshotLimit          *int         `yaml:"snapshot_limit"`
	AutomaticSnapshotLimit *int         `yaml:"automatic_snapshot_limit"`
	SubaccountsLimit       int          `yaml:"subaccounts_limit"`
	Deprecation            *Deprecation `yaml:"deprecation"`
	Prices                 []BoxPrice   `yaml:"prices"`
}

// Image is a system image (debian-13). The lab offers only the newest
// version of each system, as later phases build servers from them.
type Image struct {
	ID           int64        `yaml:"id"`
	Name         string       `yaml:"name"`
	Description  string       `yaml:"description"`
	OSFlavor     string       `yaml:"os_flavor"`
	OSVersion    string       `yaml:"os_version"`
	Architecture string       `yaml:"architecture"`
	DiskSize     float64      `yaml:"disk_size"` // GB
	Created      time.Time    `yaml:"created"`
	RapidDeploy  bool         `yaml:"rapid_deploy"`
	Deprecation  *Deprecation `yaml:"deprecation"`
	// Source is the lab's own key, not Hetzner's: where the image cache
	// loads the image from.
	Source ImageSource `yaml:"source"`
}

// ImageSource is an official cloud image: a qcow2 file and the list of
// checksums that names it (SHA256SUMS or SHA512SUMS, as `sha256sum`
// writes them).
type ImageSource struct {
	URL       string `yaml:"url"`
	Checksums string `yaml:"checksums"`
}

// ISO is a public ISO that servers can mount.
type ISO struct {
	ID          int64  `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Architecture is empty for an ISO that fits every architecture, which
	// the API shows as null.
	Architecture string       `yaml:"architecture"`
	Deprecation  *Deprecation `yaml:"deprecation"`
}

// IPPrice is the price of a Floating or Primary IP in a location; Floating
// IPs have no hourly price.
type IPPrice struct {
	Location     string `yaml:"location"`
	PriceHourly  *Price `yaml:"price_hourly"`
	PriceMonthly Price  `yaml:"price_monthly"`
}

// IPPrices are the prices of one type of IP (ipv4, ipv6).
type IPPrices struct {
	Type   string    `yaml:"type"`
	Prices []IPPrice `yaml:"prices"`
}

// PerGBMonth is a price per GB and month (images, volumes).
type PerGBMonth struct {
	PricePerGBMonth Price `yaml:"price_per_gb_month"`
}

// Pricing holds the prices that belong to no type: the rest of Hetzner's
// GET /pricing, in its shape.
type Pricing struct {
	Currency     string     `yaml:"currency"`
	VATRate      string     `yaml:"vat_rate"`
	Image        PerGBMonth `yaml:"image"`
	Volume       PerGBMonth `yaml:"volume"`
	ServerBackup struct {
		Percentage string `yaml:"percentage"`
	} `yaml:"server_backup"`
	// FloatingIP is the deprecated single price of a Floating IP, which the
	// API still sends.
	FloatingIP struct {
		PriceMonthly Price `yaml:"price_monthly"`
	} `yaml:"floating_ip"`
	FloatingIPs []IPPrices `yaml:"floating_ips"`
	PrimaryIPs  []IPPrices `yaml:"primary_ips"`
}

// UserFile is the name of the user's catalog in the data folder.
const UserFile = "catalog.yaml"

// Builtin returns the built-in catalog.
func Builtin() *Catalog {
	catalog, err := parse(builtin, nil)
	if err != nil {
		panic("catalog: the built-in catalog is broken: " + err.Error()) // the unit tests load it
	}
	return catalog
}

// Load returns the built-in catalog changed by the user's file at path: each
// key the file names replaces the built-in value, a list as a whole (so a
// file with server_types lists every type the lab offers). Without the file,
// it is the built-in catalog.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Builtin(), nil
	}
	if err != nil {
		return nil, err
	}
	catalog, err := parse(builtin, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return catalog, nil
}

// parse reads the built-in catalog and, if given, the user's over it, and
// checks the result. Unknown keys are errors, so that a typo does not go
// unnoticed.
func parse(base, user []byte) (*Catalog, error) {
	var catalog Catalog
	if err := yaml.Load(base, &catalog, yaml.WithKnownFields()); err != nil {
		return nil, err
	}
	if user != nil {
		if err := yaml.Load(user, &catalog, yaml.WithKnownFields()); err != nil {
			return nil, err
		}
	}
	if err := catalog.check(); err != nil {
		return nil, err
	}
	// Hetzner lists by ID, whatever order a file has
	sortByID(catalog.Locations, func(l Location) int64 { return l.ID })
	sortByID(catalog.ServerTypes, func(t ServerType) int64 { return t.ID })
	sortByID(catalog.LoadBalancerTypes, func(t LoadBalancerType) int64 { return t.ID })
	sortByID(catalog.StorageBoxTypes, func(t StorageBoxType) int64 { return t.ID })
	sortByID(catalog.Images, func(i Image) int64 { return i.ID })
	sortByID(catalog.ISOs, func(i ISO) int64 { return i.ID })
	return &catalog, nil
}

// sortByID sorts the entries of a list by their IDs.
func sortByID[T any](entries []T, id func(T) int64) {
	slices.SortFunc(entries, func(a, b T) int { return cmp.Compare(id(a), id(b)) })
}

// LocationByName returns the location of that name.
func (c *Catalog) LocationByName(name string) (Location, bool) {
	for _, location := range c.Locations {
		if location.Name == name {
			return location, true
		}
	}
	return Location{}, false
}

// ServerTypeByName returns the server type of that name.
func (c *Catalog) ServerTypeByName(name string) (ServerType, bool) {
	for _, serverType := range c.ServerTypes {
		if serverType.Name == name {
			return serverType, true
		}
	}
	return ServerType{}, false
}

// ImageByID returns the system image of that ID.
func (c *Catalog) ImageByID(id int64) (Image, bool) {
	for _, image := range c.Images {
		if image.ID == id {
			return image, true
		}
	}
	return Image{}, false
}
