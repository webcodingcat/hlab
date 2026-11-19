//go:build windows

package cloud

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	imagesres "github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/resources/volumes"
	"github.com/webcodingcat/hlab/internal/store"
)

func TestCloud(t *testing.T) {
	testReload(t)
	t.Run("VOL-01", func(t *testing.T) {
		cat, err := catalog.Load("../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		dir := t.TempDir()
		st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })

		created := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
		var imageID, volumeID int64
		imagePath := filepath.Join(dir, "images", "image-left.vhdx")
		volumePath := filepath.Join(dir, "volumes", "volume-left.vhdx")
		err = st.Tx(func(tx store.Tx) error {
			p, err := store.CreateProject(tx, "p")
			if err != nil {
				return err
			}
			if imageID, err = store.NextID(tx, imagesres.Kind); err != nil {
				return err
			}
			img := imagesres.Record{
				Image: imagesres.Image{
					ID: imageID, Type: "snapshot", Status: imagesres.StatusCreating,
					Description: "left creating", Created: created, OSFlavor: "ubuntu", Architecture: "x86",
				},
				Hlab: imagesres.Internal{ProjectID: p.ID, Path: imagePath},
			}
			if err := imagesres.Put(tx, img); err != nil {
				return err
			}
			if volumeID, err = store.NextID(tx, volumes.Kind); err != nil {
				return err
			}
			vol := volumes.Record{
				Volume: volumes.Volume{
					ID: volumeID, Created: created, Name: "left", Location: []byte(`{"name":"fsn1"}`),
					Size: 10, Status: volumes.StatusCreating, Labels: map[string]string{},
				},
				Hlab: volumes.Internal{ProjectID: p.ID, Location: "fsn1", Path: volumePath},
			}
			return store.Put(tx, store.Meta{
				Kind: volumes.Kind, ID: volumeID, ProjectID: p.ID, Name: vol.Name, Labels: vol.Labels, Created: created,
			}, vol)
		})
		if err != nil {
			t.Fatalf("store records left creating: %v", err)
		}
		for _, path := range []string{imagePath, volumePath} {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		mods := []resource.Module{
			resactions.New(),
			imagesres.NewModule(imagesres.Deps{Catalog: cat, DataFolder: dir, Store: st}),
			volumes.New(volumes.Deps{Catalog: cat, Store: st, DataFolder: dir}),
		}
		if err := recoverModules(context.Background(), mods); err != nil {
			t.Fatalf("recoverModules: %v", err)
		}

		err = st.Tx(func(tx store.Tx) error {
			for kind, id := range map[string]int64{imagesres.Kind: imageID, volumes.Kind: volumeID} {
				recs, err := store.ListAll[map[string]any](tx, kind)
				if err != nil {
					return err
				}
				for _, r := range recs {
					if r["id"] == float64(id) {
						return fmt.Errorf("%s record %d left creating is still stored", kind, id)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
		for _, path := range []string{imagePath, volumePath} {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("file %s left creating still exists after recovery (stat: %v)", path, err)
			}
		}
	})
}
