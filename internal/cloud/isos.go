//go:build windows

package cloud

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// problemCustomISOs is the /status problem of own ISOs that are left out.
const problemCustomISOs = "custom_isos"

// ensureCustomISOs creates the own ISOs file in configDir with an empty
// list when it is missing and gives it the access rules of settings.toml,
// so the tray can write it. An existing file is kept.
func ensureCustomISOs(configDir string) error {
	return ensureConfigList(configDir, catalog.OwnISOsFile)
}

// loadOwnISOs serves the own ISOs of the own ISOs file, deletes the cache
// files of own ISOs no longer served nor attached, and copies the local
// files of the served ones into the cache in the background; a copy that
// fails leaves out only its ISO.
func (c *Cloud) loadOwnISOs(ctx context.Context) {
	for _, w := range c.cat.ReloadOwnISOs(filepath.Join(c.d.Paths.Config, catalog.OwnISOsFile)) {
		slog.Warn("own ISO problem", "message", w)
	}
	if c.st == nil || c.images == nil {
		return // no state and image store yet: nothing to clean or copy
	}
	if err := c.cleanOwnISOs(filepath.Join(c.set.Storage.DataFolder, "isos")); err != nil {
		slog.Warn("clean the own ISO cache", "err", err)
	}
	bg := context.WithoutCancel(ctx)
	for _, iso := range c.cat.OwnISOs() {
		if iso.OwnSource.Path == "" {
			continue
		}
		go func() {
			if _, err := c.images.EnsureISO(bg, iso.CacheName); err != nil {
				slog.Warn("copy own ISO", "id", iso.ID, "path", iso.OwnSource.Path, "err", err)
				c.cat.DropOwnISO(iso.ID, "copy failed: "+err.Error())
			}
		}()
	}
}

// cleanOwnISOs deletes the own-*.iso files in dir that no served own ISO
// names and no server has attached; catalog ISO files stay.
func (c *Cloud) cleanOwnISOs(dir string) error {
	keep := map[string]bool{}
	for _, iso := range c.cat.AllISOs() {
		keep[iso.CacheName] = true
	}
	err := c.st.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[servers.Record](tx, servers.Kind)
		for _, r := range recs {
			keep[r.Hlab.ISO] = true
		}
		return err
	})
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		n := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(n, "own-") || !strings.HasSuffix(n, ".iso") || keep[n] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			slog.Warn("delete own ISO cache file", "file", n, "err", err)
			continue
		}
		slog.Info("own ISO cache file deleted", "file", n)
	}
	return nil
}
