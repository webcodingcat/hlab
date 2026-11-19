//go:build windows

package cloud

import (
	"context"
	"errors"

	"github.com/webcodingcat/hlab/internal/resource"
)

// recoverModules calls Recover of every module implementing
// resource.Recoverer, in module order, and joins their errors.
func recoverModules(ctx context.Context, mods []resource.Module) error {
	var errs []error
	for _, m := range mods {
		if r, ok := m.(resource.Recoverer); ok {
			if err := r.Recover(ctx); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
