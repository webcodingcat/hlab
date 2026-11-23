//go:build windows

package cloud

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/settings"
)

// customServerTypesFile is the file of the own server types in the
// config folder.
const customServerTypesFile = "server-types.custom.json"

// ensureCustomServerTypes creates the own server types file in configDir
// with an empty list when it is missing and gives it the access rules of
// settings.toml, so the tray can write it. An existing file is kept.
func ensureCustomServerTypes(configDir string) error {
	return ensureConfigList(configDir, customServerTypesFile)
}

// ensureConfigList creates the file name in configDir with an empty JSON
// list when it is missing and gives it the access rules of settings.toml.
// An existing file is kept.
func ensureConfigList(configDir, name string) error {
	path := filepath.Join(configDir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.Write([]byte("[]\n"))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return settings.CopySecurity(filepath.Join(configDir, "settings.toml"), path)
}

// reload applies a changed settings file, own server types file, own
// storage box types file and own ISOs file to the running cloud:
// logging.level at once, the own server types with their
// custom_server_types problem, the own storage box types with their
// custom_storage_box_types problem, the own ISOs with their
// copies, cache cleanup and custom_isos problem. stop_behaviour is read again at the stop;
// public_range, data_folder and the reserves apply at the next start. An
// invalid settings file changes nothing and returns its error.
func (c *Cloud) reload(ctx context.Context) error {
	set, err := settings.Load(filepath.Join(c.d.Paths.Config, "settings.toml"))
	if err != nil {
		return err
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(set.Logging.Level)); err == nil {
		c.d.Level.Set(l)
	}
	c.cat.ReloadOwn(filepath.Join(c.d.Paths.Config, customServerTypesFile))
	c.cat.ReloadOwnStorageBoxTypes(filepath.Join(c.d.Paths.Config, catalog.OwnStorageBoxTypesFile))
	c.loadOwnISOs(ctx)
	slog.Info("settings reloaded", "logging_level", set.Logging.Level, "own_server_types", len(c.cat.Own()),
		"own_isos", len(c.cat.OwnISOs()))
	return nil
}
