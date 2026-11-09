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
	Ports Ports `yaml:"ports"`
	// ShutdownTimeout is how many seconds the servers' guests may take to
	// shut down when the lab stops, before they are turned off.
	ShutdownTimeout int  `yaml:"shutdown_timeout"`
	Room            Room `yaml:"room"`
}

// Room bounds what the servers may take of the host; a create or a start
// beyond it is Hetzner's 412 resource_unavailable. A small host may lower
// the reserves.
type Room struct {
	// MemoryReservePercent and MemoryReserveMinMB are the host's memory that
	// the servers leave free: the share, but at least the minimum.
	MemoryReservePercent int `yaml:"memory_reserve_percent"`
	MemoryReserveMinMB   int `yaml:"memory_reserve_min_mb"`
	// CoresPerThread is how many virtual cores the servers may have per
	// thread of the host.
	CoresPerThread int `yaml:"cores_per_thread"`
	// DiskFreePercent is the share of the data folder's drive that a create
	// leaves free.
	DiskFreePercent int `yaml:"disk_free_percent"`
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
		Ports:           Ports{Cloud: 8080, Hetzner: 8081, Control: 8090},
		ShutdownTimeout: 60,
		Room:            Room{MemoryReservePercent: 25, MemoryReserveMinMB: 6 * 1024, CoresPerThread: 4, DiskFreePercent: 10},
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
	if c.ShutdownTimeout < 1 {
		return fmt.Errorf("shutdown_timeout is %d: use 1 or more seconds", c.ShutdownTimeout)
	}
	for _, percent := range []struct {
		name  string
		value int
	}{{"memory_reserve_percent", c.Room.MemoryReservePercent}, {"disk_free_percent", c.Room.DiskFreePercent}} {
		if percent.value < 0 || percent.value > 90 {
			return fmt.Errorf("room.%s is %d: use 0 to 90", percent.name, percent.value)
		}
	}
	if c.Room.MemoryReserveMinMB < 0 {
		return fmt.Errorf("room.memory_reserve_min_mb is %d: use 0 or more", c.Room.MemoryReserveMinMB)
	}
	if c.Room.CoresPerThread < 1 {
		return fmt.Errorf("room.cores_per_thread is %d: use 1 or more", c.Room.CoresPerThread)
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
