// Package discard decides whether an incompatible state is discarded at the
// start and removes the state together with the host objects it owned.
package discard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// Param is the start parameter that asks for a discard.
const Param = "discard-state"

// Options names what Run discards.
type Options struct {
	StatePath, DataFolder, VMPrefix string // VMPrefix e.g. "hlab-dev-server-"
	Platform                        platform.Platform
	Now                             time.Time
}

// Requested reports whether the start parameters ask for a discard and the
// open error is an *store.IncompatibleError; a compatible state is never
// discarded.
func Requested(params []string, openErr error) bool {
	var ie *store.IncompatibleError
	return slices.Contains(params, Param) && errors.As(openErr, &ie)
}

// Run deletes every VM with VMPrefix, the folders disks, volumes, vms and
// images\image-*.vhdx, then renames the state (store.Discard); the image and
// ISO cache, the appliance and the CA stay. The state is renamed last so a
// failed run leaves it incompatible and a retry repeats the cleanup.
func Run(ctx context.Context, o Options) (string, error) {
	vms, err := o.Platform.VMs(ctx, o.VMPrefix)
	if err != nil {
		return "", fmt.Errorf("list server VMs: %w", err)
	}
	for _, name := range vms {
		if err := o.Platform.DeleteVM(ctx, name); err != nil {
			var pe *platform.Error
			if !errors.As(err, &pe) || pe.Kind != platform.NotFound {
				return "", fmt.Errorf("delete VM %s: %w", name, err)
			}
		}
	}
	for _, dir := range []string{"disks", "volumes", "vms"} {
		if err := os.RemoveAll(filepath.Join(o.DataFolder, dir)); err != nil {
			return "", fmt.Errorf("delete %s: %w", dir, err)
		}
	}
	images, err := filepath.Glob(filepath.Join(o.DataFolder, "images", "image-*.vhdx"))
	if err != nil {
		return "", err
	}
	for _, p := range images {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("delete %s: %w", p, err)
		}
	}
	renamed, err := store.Discard(o.StatePath, o.Now)
	if err != nil {
		return "", fmt.Errorf("discard state: %w", err)
	}
	return renamed, nil
}
