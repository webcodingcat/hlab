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
	Cloud        Cloud        `toml:"cloud"`
	Network      Network      `toml:"network"`
	Storage      Storage      `toml:"storage"`
	Resources    Resources    `toml:"resources"`
	Certificates Certificates `toml:"certificates"`
	Logging      Logging      `toml:"logging"`
}

// Cloud is the table [cloud].
type Cloud struct {
	StopBehaviour string `toml:"stop_behaviour"` // "save" | "shutdown"
}

// Network is the table [network].
type Network struct {
	PublicRange netip.Prefix `toml:"public_range"`
}

// Storage is the table [storage].
type Storage struct {
	DataFolder string `toml:"data_folder"`
}

// Resources is the table [resources].
type Resources struct {
	ReserveRAMMB  int `toml:"reserve_ram_mb"`
	ReserveDiskGB int `toml:"reserve_disk_gb"`
}

// Certificates is the table [certificates].
type Certificates struct {
	TrustCA bool `toml:"trust_ca"`
}

// Logging is the table [logging].
type Logging struct {
	Level string `toml:"level"` // "debug" | "info" | "warn" | "error"
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
	if err := s.validate(); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
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
	if err := copySecurity(path, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("keep permissions of %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func (s Settings) validate() error {
	switch s.Cloud.StopBehaviour {
	case "save", "shutdown":
	default:
		return fmt.Errorf(`cloud.stop_behaviour must be "save" or "shutdown", not %q`, s.Cloud.StopBehaviour)
	}
	switch s.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf(`logging.level must be "debug", "info", "warn" or "error", not %q`, s.Logging.Level)
	}
	if !s.Network.PublicRange.Addr().Is4() {
		return fmt.Errorf("network.public_range must be an IPv4 range, not %s", s.Network.PublicRange)
	}
	if s.Storage.DataFolder == "" {
		return errors.New("storage.data_folder must not be empty")
	}
	if s.Resources.ReserveRAMMB < 0 || s.Resources.ReserveDiskGB < 0 {
		return errors.New("resources.reserve_ram_mb and resources.reserve_disk_gb must not be negative")
	}
	return nil
}

func programData() string {
	if d := os.Getenv("ProgramData"); d != "" {
		return d
	}
	return `C:\ProgramData`
}
