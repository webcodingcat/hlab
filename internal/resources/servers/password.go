package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

// defaultResetTimeout bounds the wait for the guest's confirmation of a
// password reset when Deps.ResetTimeout is 0.
const defaultResetTimeout = 120 * time.Second

// resetPollInterval is how often the action asks the agent for confirmed
// password resets.
const resetPollInterval = time.Second

// errNotRunning refuses reset_password on a server that is not running.
var errNotRunning = &api.FieldError{Code: "conflict", Message: "the server must be running to reset its root password"}

// resetPassword sets a new random root password on a running server: only
// its SHA-512 crypt goes to the agent's metadata service, where the guest
// hook fetches, applies and confirms it. The action ends with success on
// the confirmation and with server_error when none arrives in time; either
// way the pending reset is cleared. The reset's ID is the action's ID.
func (m *Module) resetPassword(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())

	password, hash, err := newRootPassword()
	if err != nil {
		api.ServerError(w, err)
		return
	}
	var action *actions.Action
	err = stor.Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		if r.Status != StatusRunning {
			return errNotRunning
		}
		action, err = acts.Start(tx, p.ID, "reset_password", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		r.Hlab.PasswordReset = &agentapi.PasswordReset{ID: action.ID, SHA512Crypt: hash}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}

	resetID := action.ID
	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		err := m.d.PushAgent(ctx)
		if err == nil {
			progress(50)
			err = m.awaitReset(ctx, resetID)
		}
		if cerr := m.clearReset(ctx, stor, id, resetID); cerr != nil && err == nil {
			err = cerr
		}
		return err
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action, "root_password": password})
}

// awaitReset asks the agent every second whether a guest confirmed reset
// id, until it did or the reset timeout passed.
func (m *Module) awaitReset(ctx context.Context, id int64) error {
	timeout := m.d.ResetTimeout
	if timeout == 0 {
		timeout = defaultResetTimeout
	}
	deadline := m.d.Clock.After(timeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("the server did not confirm the password reset within %s", timeout)
		case <-m.d.Clock.After(resetPollInterval):
		}
		st, err := m.d.AgentStatus(ctx)
		if err != nil {
			slog.Warn("agent status for a password reset", "reset", id, "err", err)
			continue
		}
		if slices.Contains(st.PasswordResetsDone, id) {
			return nil
		}
	}
}

// clearReset removes reset resetID from server id, if it is still the
// pending one, and pushes the agent document without it.
func (m *Module) clearReset(ctx context.Context, stor *store.Store, id, resetID int64) error {
	err := stor.Tx(func(tx store.Tx) error {
		r, err := Load(tx, id)
		if err != nil {
			return err
		}
		if r.Hlab.PasswordReset == nil || r.Hlab.PasswordReset.ID != resetID {
			return nil
		}
		r.Hlab.PasswordReset = nil
		return put(tx, r)
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// The action may have ended by its context; the push still has to
	// take the reset away from the guest.
	return m.d.PushAgent(context.WithoutCancel(ctx))
}
