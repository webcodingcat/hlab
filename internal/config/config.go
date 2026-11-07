// Package config reads hlab.yaml, the lab's settings in the data folder. A
// missing file, or a key the file leaves out, takes the default.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"
)

// File is the name of the settings file in the data folder.
const File = "hlab.yaml"

// Config is what hlab.yaml sets.
type Config struct {
	// TimeScale is the factor on Hetzner's timings: 1 is as fast as Hetzner,
	// 0.5 twice as fast, 0 does not wait at all.
	TimeScale float64 `yaml:"time_scale"`
	Ports     Ports   `yaml:"ports"`
}

// Ports are the TCP ports the service listens on. A port of 0 takes any free
// one (the tests); `hlab status` and `hlab env` report the ports in use.
type Ports struct {
	Cloud   int `yaml:"cloud"`   // the Cloud API
	Hetzner int `yaml:"hetzner"` // the Hetzner API (Storage Boxes)
	Control int `yaml:"control"` // the lab control and the web UI
}

// Default returns the settings without a file.
func Default() Config {
	return Config{
		TimeScale: 1,
		Ports:     Ports{Cloud: 8080, Hetzner: 8081, Control: 8090},
	}
}

// Load reads hlab.yaml in the data folder over the defaults. Unknown keys and
// values out of range stop the start, naming the file, so that a typo does
// not go unnoticed.
func Load(dataDir string) (Config, error) {
	cfg := Default()
	path := filepath.Join(dataDir, File)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	if err := yaml.Load(data, &cfg, yaml.WithKnownFields()); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.check(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// check refuses values the lab cannot run with.
func (c Config) check() error {
	if c.TimeScale < 0 {
		return fmt.Errorf("time_scale is %g: use 0 or more", c.TimeScale)
	}
	seen := map[int]string{}
	for _, port := range []struct {
		name  string
		value int
	}{{"cloud", c.Ports.Cloud}, {"hetzner", c.Ports.Hetzner}, {"control", c.Ports.Control}} {
		if port.value < 0 || port.value > 65535 {
			return fmt.Errorf("ports.%s is %d: use 1 to 65535, or 0 for any free port", port.name, port.value)
		}
		if other, taken := seen[port.value]; taken && port.value != 0 {
			return fmt.Errorf("ports.%s and ports.%s are both %d", other, port.name, port.value)
		}
		seen[port.value] = port.name
	}
	return nil
}
