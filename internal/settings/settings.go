// Package settings reads settings.toml. Every key is optional; a missing
// file gives the defaults.
package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/webcodingcat/hlab/internal/profile"
)

// Settings is the content of settings.toml, one struct per table.
type Settings struct {
	Cloud        Cloud        `toml:"cloud" json:"cloud"`
	Network      Network      `toml:"network" json:"network"`
	Storage      Storage      `toml:"storage" json:"storage"`
	Resources    Resources    `toml:"resources" json:"resources"`
	Certificates Certificates `toml:"certificates" json:"certificates"`
	Logging      Logging      `toml:"logging" json:"logging"`
}

// Cloud is the table [cloud].
type Cloud struct {
	StopBehaviour string `toml:"stop_behaviour" json:"stop_behaviour"` // "save" | "shutdown"
}

// Network is the table [network].
type Network struct {
	PublicRange netip.Prefix `toml:"public_range" json:"public_range"`
}

// Storage is the table [storage].
type Storage struct {
	DataFolder string `toml:"data_folder" json:"data_folder"`
}

// Resources is the table [resources].
type Resources struct {
	ReserveRAMMB  int `toml:"reserve_ram_mb" json:"reserve_ram_mb"`
	ReserveDiskGB int `toml:"reserve_disk_gb" json:"reserve_disk_gb"`
}

// Certificates is the table [certificates].
type Certificates struct {
	TrustCA bool `toml:"trust_ca" json:"trust_ca"`
}

// Logging is the table [logging].
type Logging struct {
	Level string `toml:"level" json:"level"` // "debug" | "info" | "warn" | "error"
}

// Defaults returns the settings without a settings file.
func Defaults() Settings {
	return Settings{
		Cloud:     Cloud{StopBehaviour: "save"},
		Network:   Network{PublicRange: netip.MustParsePrefix("198.19.0.0/16")},
		Storage:   Storage{DataFolder: filepath.Join(programData(), profile.Current().DataDirName, "data")},
		Resources: Resources{ReserveRAMMB: 4096, ReserveDiskGB: 50},
		Logging:   Logging{Level: "info"},
	}
}

// Load reads the settings file at path over the defaults. A missing file
// gives the defaults; an unknown key or an invalid value is an error.
func Load(path string) (Settings, error) {
	s := Defaults()
	md, err := toml.DecodeFile(path, &s)
	if errors.Is(err, fs.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read %s: %w", path, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, len(u))
		for i, k := range u {
			keys[i] = k.String()
		}
		return Settings{}, fmt.Errorf("%s: unknown key %s", path, strings.Join(keys, ", "))
	}
	if err := Validate(s); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", path, err)
	}
	s.Network.PublicRange = s.Network.PublicRange.Masked()
	return s, nil
}

// Write validates s and writes it to path in place, in one write, so the
// file keeps its access rules. trust_ca is taken from the file at path
// (PUT /trust owns it); a missing file gives false. An invalid s returns a
// *FieldError and leaves the file unchanged.
func Write(path string, s Settings) error {
	if err := Validate(s); err != nil {
		return err
	}
	var cur struct {
		Certificates Certificates `toml:"certificates"`
	}
	if _, err := toml.DecodeFile(path, &cur); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	s.Certificates.TrustCA = cur.Certificates.TrustCA
	s.Network.PublicRange = s.Network.PublicRange.Masked()
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(s); err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Save writes s to path as TOML, replacing the whole file; comments in an
// existing file are lost. It writes a temporary file beside path first and
// renames it, so a failed write leaves the old file. The new file keeps the
// access rules of the file it replaces.
func Save(path string, s Settings) error {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(s); err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := CopySecurity(path, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("keep permissions of %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// FieldError is an invalid settings value; Key is its settings key, for
// example "network.public_range".
type FieldError struct{ Key, Message string }

func (e *FieldError) Error() string { return e.Key + " " + e.Message }

// Validate checks s against the rules Load applies; it returns nil or a
// *FieldError for the first invalid value.
func Validate(s Settings) error {
	switch s.Cloud.StopBehaviour {
	case "save", "shutdown":
	default:
		return &FieldError{"cloud.stop_behaviour", fmt.Sprintf(`must be "save" or "shutdown", not %q`, s.Cloud.StopBehaviour)}
	}
	switch s.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return &FieldError{"logging.level", fmt.Sprintf(`must be "debug", "info", "warn" or "error", not %q`, s.Logging.Level)}
	}
	if err := validateRange(s.Network.PublicRange); err != nil {
		return err
	}
	if s.Storage.DataFolder == "" {
		return &FieldError{"storage.data_folder", "must not be empty"}
	}
	if s.Resources.ReserveRAMMB < 0 {
		return &FieldError{"resources.reserve_ram_mb", "must not be negative"}
	}
	if s.Resources.ReserveDiskGB < 0 {
		return &FieldError{"resources.reserve_disk_gb", "must not be negative"}
	}
	return nil
}

// excludedRanges may not overlap network.public_range: reserved, private
// and shared ranges, multicast and above, and Hetzner's resolvers.
var excludedRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/3"),
	netip.MustParsePrefix("185.12.64.1/32"),
	netip.MustParsePrefix("185.12.64.2/32"),
}

func validateRange(r netip.Prefix) error {
	const key = "network.public_range"
	if !r.Addr().Is4() {
		return &FieldError{key, fmt.Sprintf("must be an IPv4 range, not %s", r)}
	}
	if r.Bits() < 8 || r.Bits() > 28 {
		return &FieldError{key, fmt.Sprintf("must be a /8 to /28 range, not %s", r)}
	}
	for _, x := range excludedRanges {
		if x.Overlaps(r) {
			return &FieldError{key, fmt.Sprintf("%s overlaps %s, which HLab cannot use", r.Masked(), x)}
		}
	}
	return nil
}

func programData() string {
	if d := os.Getenv("ProgramData"); d != "" {
		return d
	}
	return `C:\ProgramData`
}
