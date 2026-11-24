package faults

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/webcodingcat/hlab/internal/api"
)

// kindTargets are the target types each kind takes; an empty list means
// no target (the project).
var kindTargets = map[string][]string{
	KindServerCrash:           {TargetServer},
	KindServerFreeze:          {TargetServer},
	KindNetwork:               {TargetServer},
	KindLatency:               {TargetServer, TargetLocations},
	KindAPIError:              nil,
	KindRateLimit:             nil,
	KindVolumeUnavailable:     {TargetVolume},
	KindDiskSlow:              {TargetVolume, TargetServer},
	KindStorageBoxUnavailable: {TargetStorageBox},
}

const (
	minDuration = time.Second
	maxDuration = 24 * time.Hour
)

func checkDuration(d time.Duration) error {
	if d < minDuration || d > maxDuration {
		return &InputError{Field: "duration", Message: fmt.Sprintf("duration %s is not between 1s and 24h", d)}
	}
	return nil
}

// validate checks s against the kind's target, parameters, seed and
// duration rules and returns the parameters with numbers as int64.
func (m *Manager) validate(s Spec) (map[string]any, error) {
	targets, known := kindTargets[s.Kind]
	if !known {
		return nil, &InputError{Field: "kind", Message: fmt.Sprintf("unknown kind %q", s.Kind)}
	}
	if s.Kind != KindAPIError && s.Kind != KindRateLimit && m.effects[s.Kind] == nil {
		return nil, &InputError{Field: "kind", Message: fmt.Sprintf("kind %s is not available yet", s.Kind)}
	}
	if err := checkTarget(s.Kind, s.Target, targets); err != nil {
		return nil, err
	}
	p := params{in: s.Params, out: map[string]any{}}
	switch s.Kind {
	case KindNetwork:
		p.oneOf("traffic", true, "public", "private", "all")
		p.intRange("loss_percent", true, 1, 100)
	case KindLatency:
		p.intRange("delay_ms", true, 1, 10000)
		if delay, ok := p.out["delay_ms"].(int64); ok {
			p.intRange("jitter_ms", false, 0, delay)
		}
	case KindAPIError:
		p.code("code")
		p.intRange("percent", true, 1, 100)
		p.oneOf("only", false, "read", "write")
		p.oneOf("resource", false, m.resources...)
	case KindRateLimit:
		p.intRange("per_hour", true, 1, 3600)
	case KindDiskSlow:
		p.intRange("iops", true, 10, 1000000)
	}
	p.unknown()
	if p.err != nil {
		return nil, p.err
	}
	if s.Seed != nil && !takesSeed(s.Kind, p.out) {
		return nil, &InputError{Field: "seed", Message: "kind " + s.Kind + " takes no seed (only api_error and network with loss_percent below 100)"}
	}
	if s.Duration != 0 {
		if err := checkDuration(s.Duration); err != nil {
			return nil, err
		}
	}
	return p.out, nil
}

// takesSeed reports whether a fault of kind with params draws random
// numbers.
func takesSeed(kind string, params map[string]any) bool {
	switch kind {
	case KindAPIError:
		return true
	case KindNetwork:
		loss, _ := params["loss_percent"].(int64)
		return loss < 100
	}
	return false
}

func checkTarget(kind string, t *Target, types []string) error {
	if len(types) == 0 {
		if t != nil {
			return &InputError{Field: "target", Message: "kind " + kind + " takes no target"}
		}
		return nil
	}
	if t == nil {
		return &InputError{Field: "target", Message: "kind " + kind + " needs a target"}
	}
	if !slices.Contains(types, t.Type) {
		return &InputError{Field: "target.type", Message: fmt.Sprintf("kind %s takes a target of type %v, not %q", kind, types, t.Type)}
	}
	if t.Type == TargetLocations {
		switch {
		case t.From == "":
			return &InputError{Field: "target.from", Message: "missing location"}
		case t.To == "":
			return &InputError{Field: "target.to", Message: "missing location"}
		case t.From == t.To:
			return &InputError{Field: "target.to", Message: "from and to are the same location"}
		case t.ID != 0:
			return &InputError{Field: "target.id", Message: "a locations target takes no id"}
		}
		return nil
	}
	if t.ID <= 0 {
		return &InputError{Field: "target.id", Message: "missing or invalid id"}
	}
	if t.From != "" || t.To != "" {
		return &InputError{Field: "target.from", Message: "a " + t.Type + " target takes no locations"}
	}
	return nil
}

// params validates the parameters of one request; the first error sticks.
type params struct {
	in   map[string]any
	out  map[string]any
	seen []string
	err  error
}

func (p *params) fail(name, format string, a ...any) {
	if p.err == nil {
		p.err = &InputError{Field: "params." + name, Message: fmt.Sprintf(format, a...)}
	}
}

// get returns the parameter name, marking it as known.
func (p *params) get(name string, required bool) (any, bool) {
	p.seen = append(p.seen, name)
	v, ok := p.in[name]
	if !ok || v == nil {
		if required {
			p.fail(name, "missing")
		}
		return nil, false
	}
	return v, true
}

func (p *params) intRange(name string, required bool, lo, hi int64) {
	v, ok := p.get(name, required)
	if !ok {
		return
	}
	n, isInt := toInt64(v)
	if !isInt {
		p.fail(name, "%v is not an integer", v)
		return
	}
	if n < lo || n > hi {
		p.fail(name, "%d is not between %d and %d", n, lo, hi)
		return
	}
	p.out[name] = n
}

// oneOf checks a string parameter against allowed; nil allowed means any.
func (p *params) oneOf(name string, required bool, allowed ...string) {
	v, ok := p.get(name, required)
	if !ok {
		return
	}
	s, isString := v.(string)
	if !isString {
		p.fail(name, "%v is not a string", v)
		return
	}
	if allowed != nil && !slices.Contains(allowed, s) {
		p.fail(name, "%q is not one of %v", s, allowed)
		return
	}
	p.out[name] = s
}

// code checks a Hetzner error code of api.Status.
func (p *params) code(name string) {
	v, ok := p.get(name, true)
	if !ok {
		return
	}
	s, isString := v.(string)
	if !isString || !api.HasStatus(s) {
		p.fail(name, "%v is not a Hetzner error code", v)
		return
	}
	p.out[name] = s
}

// unknown refuses parameters the kind does not take.
func (p *params) unknown() {
	for name := range p.in {
		if !slices.Contains(p.seen, name) {
			p.fail(name, "unknown parameter")
		}
	}
}

// toInt64 converts the number types of Go literals and JSON decoding.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint64:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case float64:
		if n != math.Trunc(n) || n < math.MinInt64 || n >= math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}
