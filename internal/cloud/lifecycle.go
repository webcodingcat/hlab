//go:build windows

package cloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"time"

	"github.com/webcodingcat/hlab/internal/discard"
	"github.com/webcodingcat/hlab/internal/hostnet"
	"github.com/webcodingcat/hlab/internal/ipam"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/settings"
	"github.com/webcodingcat/hlab/internal/store"
)

// Problem codes of a refused start because of the state.
const (
	problemStateIncompatible = "state_incompatible"
	problemRangeChanged      = "public_range_changed"
)

// discardHint ends the message of a refusal the user can resolve by
// discarding the state.
const discardHint = "start with hlab cloud start --discard-state to discard it"

// openState opens the state. An incompatible state, or one holding public
// IPv4s outside the configured range, refuses the start unless params ask
// for a discard; then the old state is discarded with its host objects and
// an empty state is opened. The range of a started state is recorded.
func (c *Cloud) openState(ctx context.Context, params []string) error {
	path := filepath.Join(c.d.Paths.State, "state.db")
	discarded := false
	st, err := store.Open(path)
	if err != nil {
		var ie *store.IncompatibleError
		if !errors.As(err, &ie) {
			return err
		}
		if !discard.Requested(params, err) {
			return &refusal{hostnet.Problem{Code: problemStateIncompatible, Message: incompatibleMessage(ie), Fatal: true}}
		}
		if st, err = c.discardState(ctx, path); err != nil {
			return err
		}
		discarded = true
	}
	c.st = st
	msg, err := c.rangeChanged()
	if err != nil {
		return err
	}
	if msg != "" && slices.Contains(params, discard.Param) {
		_ = c.st.Close()
		c.st = nil
		if c.st, err = c.discardState(ctx, path); err != nil {
			return err
		}
		discarded, msg = true, ""
	}
	if msg != "" {
		return &refusal{hostnet.Problem{Code: problemRangeChanged, Message: msg, Fatal: true}}
	}
	if !discarded && slices.Contains(params, discard.Param) {
		slog.Info("discard of the state requested, but the state is compatible; it is kept")
	}
	return c.st.Tx(func(tx store.Tx) error { return ipam.RecordRange(tx, c.set.Network.PublicRange) })
}

// rangeChanged returns the refusal message when the state holds public
// IPv4s outside the configured range, "" otherwise.
func (c *Cloud) rangeChanged() (string, error) {
	r := c.set.Network.PublicRange
	var used, recorded string
	err := c.st.Tx(func(tx store.Tx) error {
		out, old, err := ipam.Outside(tx, r)
		if len(out) > 0 {
			used = out[0].String()
		}
		if old.IsValid() {
			recorded = old.String()
		}
		return err
	})
	if err != nil {
		return "", fmt.Errorf("check the public range: %w", err)
	}
	switch {
	case used == "":
		return "", nil
	case recorded != "":
		return fmt.Sprintf("network.public_range changed from %s to %s, but the state holds addresses of the old range; "+
			"set it back or %s", recorded, r, discardHint), nil
	default:
		return fmt.Sprintf("the state holds address %s outside network.public_range %s; set the range back or %s",
			used, r, discardHint), nil
	}
}

// incompatibleMessage is the refusal message of an incompatible state.
func incompatibleMessage(ie *store.IncompatibleError) string {
	switch {
	case ie.NotDatabase:
		return "the state file is not a readable hlab state; " + discardHint
	case ie.Newer():
		return fmt.Sprintf("the state was written by a newer hlab version (state version %d, this version reads %d); %s",
			ie.Found, ie.Want, discardHint)
	default:
		return fmt.Sprintf("the state has an unknown version (state version %d, this version reads %d); %s",
			ie.Found, ie.Want, discardHint)
	}
}

// discardState removes the old state's host objects (hostnet.Down: tagged
// objects and zone NRPT rules), its server VMs and files, and renames the
// state file; it returns the new, empty state.
func (c *Cloud) discardState(ctx context.Context, path string) (*store.Store, error) {
	slog.Warn("discarding the state", "path", path)
	if err := c.net.Down(ctx); err != nil {
		return nil, err
	}
	renamed, err := discard.Run(ctx, discard.Options{
		StatePath:  path,
		DataFolder: c.set.Storage.DataFolder,
		VMPrefix:   c.tag + "-server-",
		Platform:   c.plat,
		Now:        time.Now(),
	})
	if err != nil {
		return nil, err
	}
	slog.Warn("state discarded", "renamed_to", renamed)
	return store.Open(path)
}

// restore lets every module implementing resource.Lifecycle resume or
// power on what ran at the last stop; failures are logged.
func (c *Cloud) restore(ctx context.Context) {
	for _, m := range c.mods {
		if l, ok := m.(resource.Lifecycle); ok {
			if err := l.Restore(ctx, c.set.Cloud.StopBehaviour); err != nil {
				slog.Error("restore", "module", m.Type(), "err", err)
			}
		}
	}
	c.restored = true
}

// halt saves or shuts down what runs per stop_behaviour, read again from
// the settings (the start's value if the file is invalid now).
func (c *Cloud) halt(ctx context.Context, hostShutdown bool) error {
	behaviour := c.set.Cloud.StopBehaviour
	if set, err := settings.Load(filepath.Join(c.d.Paths.Config, "settings.toml")); err == nil {
		behaviour = set.Cloud.StopBehaviour
	} else {
		slog.Warn("settings unreadable at stop, using the stop behaviour of the start", "err", err, "stop_behaviour", behaviour)
	}
	slog.Info("halting servers", "stop_behaviour", behaviour, "host_shutdown", hostShutdown)
	var errs []error
	for _, m := range c.mods {
		if l, ok := m.(resource.Lifecycle); ok {
			if err := l.Halt(ctx, behaviour, hostShutdown); err != nil {
				errs = append(errs, fmt.Errorf("%s: halt: %w", m.Type(), err))
			}
		}
	}
	return errors.Join(errs...)
}
