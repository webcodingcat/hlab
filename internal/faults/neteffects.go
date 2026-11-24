package faults

import (
	"cmp"
	"context"
	"slices"
	"sync"
)

// NetEffects is the effect of network, latency and storage_box_unavailable.
// It keeps the net faults whose Apply runs or that are listed; Apply adds
// the fault and calls push (an error removes it again and refuses the
// fault), Undo removes it and calls push.
type NetEffects struct {
	push func(ctx context.Context) error

	mu     sync.Mutex
	faults map[int64]Fault
}

// NewNetEffects returns a NetEffects without faults that pushes the agent
// document through push.
func NewNetEffects(push func(ctx context.Context) error) *NetEffects {
	return &NetEffects{push: push, faults: map[int64]Fault{}}
}

// Effects returns the effect for the three kinds, for Options.Effects
// next to Effects(s, v).
func (n *NetEffects) Effects() map[string]Effect {
	return map[string]Effect{
		KindNetwork:               n,
		KindLatency:               n,
		KindStorageBoxUnavailable: n,
	}
}

// Faults returns the kept net faults by ID (copies).
func (n *NetEffects) Faults() []Fault {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Fault, 0, len(n.faults))
	for _, f := range n.faults {
		out = append(out, clone(f))
	}
	slices.SortFunc(out, func(a, b Fault) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Apply keeps f before pushing, so the document built during the push
// holds it; a push error drops it again.
func (n *NetEffects) Apply(ctx context.Context, f Fault) error {
	n.mu.Lock()
	n.faults[f.ID] = clone(f)
	n.mu.Unlock()
	if err := n.push(ctx); err != nil {
		n.remove(f.ID)
		return err
	}
	return nil
}

// Undo drops f and pushes the document without it.
func (n *NetEffects) Undo(ctx context.Context, f Fault) error {
	n.remove(f.ID)
	return n.push(ctx)
}

func (n *NetEffects) remove(id int64) {
	n.mu.Lock()
	delete(n.faults, id)
	n.mu.Unlock()
}
