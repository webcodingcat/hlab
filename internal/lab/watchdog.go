package lab

import (
	"context"
	"errors"
	"time"

	"github.com/webcodingcat/hlab/internal/state"
)

// watchServers looks at the servers' VMs every interval until ctx ends
// (watchRound).
func (m *Machine) watchServers(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		m.watchRound(ctx)
	}
}

// watchRound looks once at the VMs of the servers that no action works on:
// a server that runs in the record but whose VM is off has shut itself
// down, so it is off and no longer wants to run, as on Hetzner.
//
// It looks at the VMs without the mutex, so an action may start and end on
// a server meanwhile. The round therefore notes the servers it looks at in
// looks, and noteFlag marks those that an action flags during the look:
// settleServer leaves them alone. looks lives only for the round, so it
// never keeps an entry of a deleted server.
func (m *Machine) watchRound(ctx context.Context) {
	var ids []int64
	m.lab.mu.Lock()
	m.looks = map[state.ResourceRef]bool{}
	for _, server := range state.All(m.lab.read(), state.Servers) {
		if ref := serverRef(server.ID); watched(server.Status) && m.lab.flags[ref] == 0 {
			m.looks[ref] = false
			ids = append(ids, server.ID)
		}
	}
	m.lab.mu.Unlock()
	defer func() {
		m.lab.mu.Lock()
		m.looks = nil
		m.lab.mu.Unlock()
	}()
	for _, id := range ids {
		runs, err := m.system.ServerRuns(ctx, ServerVM(id))
		if err != nil {
			if ctx.Err() == nil {
				m.lab.logger.Warn("the watchdog could not look at a server's VM", "server", id, "error", err)
			}
			continue
		}
		m.settleServer(id, runs)
	}
}

// watched tells whether the watchdog looks at a server of the status: a
// running one, whose guest may shut down, and one that a failed power
// action left starting or stopping.
func watched(status state.ServerStatus) bool {
	return status == state.ServerRunning || status == state.ServerStarting || status == state.ServerStopping
}

// settleServer makes a server's status follow its VM, unless an action took
// the server over meanwhile. A running server whose VM is off shut itself
// down, so it no longer wants to run, as on Hetzner. A server that a failed
// power action left starting or stopping gets the status of its VM, and
// want_running stays as the action's plan set it. If an action has flagged
// the server since the watchdog began to look at its VM, even one that has
// ended already, what the watchdog saw is out of date.
func (m *Machine) settleServer(id int64, runs bool) {
	m.lab.mu.Lock()
	defer m.lab.mu.Unlock()
	ref := serverRef(id)
	if outOfDate, looked := m.looks[ref]; !looked || outOfDate || m.lab.flags[ref] > 0 {
		return
	}
	var before state.ServerStatus
	err := m.lab.changeLocked(func(tx *state.Tx) error {
		server, err := state.Get(tx, state.Servers, id)
		if err != nil || !watched(server.Status) {
			return err
		}
		before = server.Status
		switch {
		case server.Status == state.ServerRunning && !runs:
			server.Status, server.WantRunning = state.ServerOff, false
		case server.Status != state.ServerRunning && runs:
			server.Status = state.ServerRunning
		case server.Status != state.ServerRunning:
			server.Status = state.ServerOff
		}
		return nil
	})
	switch {
	case err != nil && !errors.Is(err, state.ErrNotFound):
		m.lab.logger.Warn("settling a server's status failed", "server", id, "error", err)
	case err == nil && before == state.ServerRunning && !runs:
		m.lab.logger.Info("the server shut down", "server", id)
	case err == nil && before != "" && before != state.ServerRunning:
		m.lab.logger.Warn("a server's status was left by a failed action; it follows its VM", "server", id,
			"was", before, "running", runs)
	}
}

// noteFlag notes that an action flags ref: if the watchdog looks at it
// now, what it sees is out of date. The caller holds the mutex.
func (m *Machine) noteFlag(ref state.ResourceRef) {
	if _, looking := m.looks[ref]; looking {
		m.looks[ref] = true
	}
}
