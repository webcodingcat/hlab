package faults_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/faults"
)

func init() {
	register("FAULT-01", testFault01)
	register("FAULT-02", testFault02)
}

func testFault01(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		e := newEnv(t)
		cases := []struct {
			kind     string
			target   *faults.Target
			params   map[string]any
			duration time.Duration
		}{
			{faults.KindServerCrash, serverTarget(7), map[string]any{}, 5 * time.Minute},
			{faults.KindServerFreeze, serverTarget(8), map[string]any{}, 0},
			{faults.KindNetwork, serverTarget(7), map[string]any{"traffic": "public", "loss_percent": 100}, time.Hour},
			{faults.KindLatency, locationsTarget("fsn1", "hel1"), map[string]any{"delay_ms": 100, "jitter_ms": 10}, 0},
			{faults.KindAPIError, nil, map[string]any{"code": "server_error", "percent": 20, "only": "write", "resource": "servers"}, 30 * time.Second},
			{faults.KindRateLimit, nil, map[string]any{"per_hour": 10}, 0},
			{faults.KindVolumeUnavailable, volumeTarget(7), map[string]any{}, 24 * time.Hour},
			{faults.KindDiskSlow, serverTarget(7), map[string]any{"iops": 100}, 0},
			{faults.KindStorageBoxUnavailable, storageBoxTarget(5), map[string]any{}, time.Second},
		}
		for i, tc := range cases {
			t.Run(tc.kind, func(t *testing.T) {
				e.Clock.Advance(time.Minute)
				now := e.Clock.Now()
				f := e.create(t, "p", faults.Spec{Kind: tc.kind, Target: tc.target, Params: tc.params, Duration: tc.duration})
				if f.ID != int64(i+1) {
					t.Errorf("ID %d, want %d", f.ID, i+1)
				}
				if f.Project != "p" || f.Kind != tc.kind {
					t.Errorf("project %q kind %q, want p %s", f.Project, f.Kind, tc.kind)
				}
				switch {
				case tc.target == nil && f.Target != nil:
					t.Errorf("target %+v, want nil", *f.Target)
				case tc.target != nil && (f.Target == nil || *f.Target != *tc.target):
					t.Errorf("target %+v, want %+v", f.Target, *tc.target)
				}
				if got, want := jsonOf(t, f.Params), jsonOf(t, tc.params); got != want {
					t.Errorf("params %s, want %s", got, want)
				}
				if !f.Created.Equal(now) {
					t.Errorf("created %v, want %v", f.Created, now)
				}
				switch {
				case tc.duration == 0 && f.Ends != nil:
					t.Errorf("ends %v, want nil", *f.Ends)
				case tc.duration != 0 && (f.Ends == nil || !f.Ends.Equal(now.Add(tc.duration))):
					t.Errorf("ends %v, want %v", f.Ends, now.Add(tc.duration))
				}
			})
		}
	})

	t.Run("seed", func(t *testing.T) {
		const chosen = "chosen"
		loss := func(p int) map[string]any { return map[string]any{"traffic": "all", "loss_percent": p} }
		cases := []struct {
			name   string
			spec   faults.Spec
			want   any // chosen, nil (no seed) or a uint64
			errors bool
		}{
			{"api_error_chosen", apiErrorSpec("server_error", 50), chosen, false},
			{"network_loss_chosen", faults.Spec{Kind: faults.KindNetwork, Target: serverTarget(7), Params: loss(50)}, chosen, false},
			{"api_error_kept", withSeed(apiErrorSpec("server_error", 50), 42), uint64(42), false},
			{"network_loss_max_kept", withSeed(faults.Spec{Kind: faults.KindNetwork, Target: serverTarget(7), Params: loss(99)}, 1<<64-1), uint64(1<<64 - 1), false},
			{"api_error_max_kept", withSeed(apiErrorSpec("server_error", 50), 1<<64-1), uint64(1<<64 - 1), false},
			{"server_crash_nil", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(7), Params: map[string]any{}}, nil, false},
			{"rate_limit_nil", rateLimitSpec(10), nil, false},
			{"network_cut_nil", faults.Spec{Kind: faults.KindNetwork, Target: serverTarget(7), Params: loss(100)}, nil, false},
			{"server_crash_given", withSeed(faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(7), Params: map[string]any{}}, 42), nil, true},
			{"rate_limit_given", withSeed(rateLimitSpec(10), 42), nil, true},
			{"disk_slow_given", withSeed(faults.Spec{Kind: faults.KindDiskSlow, Target: volumeTarget(7), Params: map[string]any{"iops": 100}}, 42), nil, true},
			{"network_cut_given", withSeed(faults.Spec{Kind: faults.KindNetwork, Target: serverTarget(7), Params: loss(100)}, 42), nil, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := newEnv(t)
				f, err := e.M.Create(t.Context(), "p", tc.spec)
				if tc.errors {
					if field := inputField(t, err); field != "seed" {
						t.Errorf("field %q, want seed", field)
					}
					if n := len(e.M.List("")); n != 0 {
						t.Errorf("%d faults listed, want 0", n)
					}
					return
				}
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				switch want := tc.want.(type) {
				case nil:
					if f.Seed != nil {
						t.Errorf("seed %d, want nil", *f.Seed)
					}
				case string:
					if f.Seed == nil || *f.Seed < 1 || *f.Seed > faults.MaxChosenSeed {
						t.Errorf("seed %v, want one in 1..%d", f.Seed, uint64(faults.MaxChosenSeed))
					}
				case uint64:
					if f.Seed == nil || *f.Seed != want {
						t.Errorf("seed %v, want %d", f.Seed, want)
					}
				}
			})
		}
	})

	t.Run("invalid_input", func(t *testing.T) {
		type tcase struct {
			name   string
			req    faults.Request
			fields []string // the field must be one of these or end in ".<one>"
			mod    func(*faults.Options)
		}
		srv := &faults.Target{Type: faults.TargetServer, ID: 7}
		vol := &faults.Target{Type: faults.TargetVolume, ID: 7}
		box := &faults.Target{Type: faults.TargetStorageBox, ID: 5}
		loc := &faults.Target{Type: faults.TargetLocations, From: "fsn1", To: "hel1"}
		req := func(kind string, target *faults.Target, params map[string]any) faults.Request {
			return faults.Request{Kind: kind, Target: target, Params: params}
		}
		apiErr := func(params map[string]any) faults.Request { return req(faults.KindAPIError, nil, params) }
		net := func(params map[string]any) faults.Request { return req(faults.KindNetwork, srv, params) }
		lat := func(params map[string]any) faults.Request { return req(faults.KindLatency, srv, params) }
		cases := []tcase{
			{name: "unknown_kind", req: req("server_explode", srv, map[string]any{}), fields: []string{"kind"}},
			{
				name: "kind_without_effect", req: req(faults.KindServerCrash, srv, map[string]any{}), fields: []string{"kind"},
				mod: func(o *faults.Options) { delete(o.Effects, faults.KindServerCrash) },
			},
		}
		// A valid target and the params of each targeted kind, and a target
		// of a type the kind does not take.
		targeted := []struct {
			kind   string
			params map[string]any
			wrong  *faults.Target
		}{
			{faults.KindServerCrash, map[string]any{}, vol},
			{faults.KindServerFreeze, map[string]any{}, box},
			{faults.KindNetwork, map[string]any{"traffic": "all", "loss_percent": 100}, vol},
			{faults.KindLatency, map[string]any{"delay_ms": 100}, box},
			{faults.KindVolumeUnavailable, map[string]any{}, srv},
			{faults.KindDiskSlow, map[string]any{"iops": 100}, box},
			{faults.KindStorageBoxUnavailable, map[string]any{}, srv},
		}
		for _, k := range targeted {
			cases = append(cases,
				tcase{name: k.kind + "_missing_target", req: req(k.kind, nil, k.params), fields: []string{"target"}},
				tcase{name: k.kind + "_wrong_target", req: req(k.kind, k.wrong, k.params), fields: []string{"target", "type"}},
			)
		}
		cases = append(cases, []tcase{
			{name: "api_error_with_target", req: req(faults.KindAPIError, srv, map[string]any{"code": "server_error", "percent": 50}), fields: []string{"target"}},
			{name: "rate_limit_with_target", req: req(faults.KindRateLimit, srv, map[string]any{"per_hour": 10}), fields: []string{"target"}},
			{name: "traffic_missing", req: net(map[string]any{"loss_percent": 100}), fields: []string{"traffic"}},
			{name: "traffic_other", req: net(map[string]any{"traffic": "both", "loss_percent": 100}), fields: []string{"traffic"}},
			{name: "loss_percent_missing", req: net(map[string]any{"traffic": "all"}), fields: []string{"loss_percent"}},
			{name: "loss_percent_0", req: net(map[string]any{"traffic": "all", "loss_percent": 0}), fields: []string{"loss_percent"}},
			{name: "loss_percent_101", req: net(map[string]any{"traffic": "all", "loss_percent": 101}), fields: []string{"loss_percent"}},
			{name: "delay_ms_missing", req: lat(map[string]any{}), fields: []string{"delay_ms"}},
			{name: "delay_ms_0", req: lat(map[string]any{"delay_ms": 0}), fields: []string{"delay_ms"}},
			{name: "delay_ms_10001", req: lat(map[string]any{"delay_ms": 10001}), fields: []string{"delay_ms"}},
			{name: "jitter_ms_above_delay", req: lat(map[string]any{"delay_ms": 100, "jitter_ms": 101}), fields: []string{"jitter_ms"}},
			{name: "code_missing", req: apiErr(map[string]any{"percent": 50}), fields: []string{"code"}},
			{name: "code_unknown", req: apiErr(map[string]any{"code": "no_such_code", "percent": 50}), fields: []string{"code"}},
			{name: "percent_missing", req: apiErr(map[string]any{"code": "server_error"}), fields: []string{"percent"}},
			{name: "percent_0", req: apiErr(map[string]any{"code": "server_error", "percent": 0}), fields: []string{"percent"}},
			{name: "percent_101", req: apiErr(map[string]any{"code": "server_error", "percent": 101}), fields: []string{"percent"}},
			{name: "only_other", req: apiErr(map[string]any{"code": "server_error", "percent": 50, "only": "delete"}), fields: []string{"only"}},
			{name: "resource_unknown", req: apiErr(map[string]any{"code": "server_error", "percent": 50, "resource": "no_such_resource"}), fields: []string{"resource"}},
			{name: "per_hour_missing", req: req(faults.KindRateLimit, nil, map[string]any{}), fields: []string{"per_hour"}},
			{name: "per_hour_0", req: req(faults.KindRateLimit, nil, map[string]any{"per_hour": 0}), fields: []string{"per_hour"}},
			{name: "per_hour_3601", req: req(faults.KindRateLimit, nil, map[string]any{"per_hour": 3601}), fields: []string{"per_hour"}},
			{name: "iops_missing", req: req(faults.KindDiskSlow, vol, map[string]any{}), fields: []string{"iops"}},
			{name: "iops_9", req: req(faults.KindDiskSlow, vol, map[string]any{"iops": 9}), fields: []string{"iops"}},
			{
				name:   "from_equals_to",
				req:    req(faults.KindLatency, &faults.Target{Type: faults.TargetLocations, From: "fsn1", To: "fsn1"}, map[string]any{"delay_ms": 100}),
				fields: []string{"target", "to", "from"},
			},
		}...)
		for _, d := range []string{"0s", "25h", "abc"} {
			r := req(faults.KindLatency, loc, map[string]any{"delay_ms": 100})
			r.Duration = d
			cases = append(cases, tcase{name: "duration_" + d, req: r, fields: []string{"duration"}})
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var mods []func(*faults.Options)
				if tc.mod != nil {
					mods = append(mods, tc.mod)
				}
				e := newEnv(t, mods...)
				s, err := tc.req.Spec()
				if err == nil {
					_, err = e.M.Create(t.Context(), "p", s)
				}
				field := inputField(t, err)
				if !fieldMatches(field, tc.fields) {
					t.Errorf("field %q, want one of %v", field, tc.fields)
				}
				if n := len(e.M.List("")); n != 0 {
					t.Errorf("%d faults listed, want 0", n)
				}
			})
		}
	})

	t.Run("unknown_target", func(t *testing.T) {
		cases := []struct {
			name     string
			spec     faults.Spec
			notFound bool
		}{
			{"server", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(99), Params: map[string]any{}}, true},
			{"volume", faults.Spec{Kind: faults.KindDiskSlow, Target: volumeTarget(99), Params: map[string]any{"iops": 100}}, true},
			{"storage_box", faults.Spec{Kind: faults.KindStorageBoxUnavailable, Target: storageBoxTarget(99), Params: map[string]any{}}, true},
			{"location", faults.Spec{Kind: faults.KindLatency, Target: locationsTarget("fsn1", "xyz1"), Params: map[string]any{"delay_ms": 100}}, true},
			{"volume_not_attached", faults.Spec{Kind: faults.KindVolumeUnavailable, Target: volumeTarget(4), Params: map[string]any{}}, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := newEnv(t)
				_, err := e.M.Create(t.Context(), "p", tc.spec)
				if tc.notFound {
					if !errors.Is(err, faults.ErrNotFound) {
						t.Errorf("error %v, want ErrNotFound", err)
					}
				} else {
					inputField(t, err)
				}
				if n := len(e.M.List("")); n != 0 {
					t.Errorf("%d faults listed, want 0", n)
				}
			})
		}
	})

	t.Run("conflict", func(t *testing.T) {
		crash := func(id int64) faults.Spec {
			return faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(id), Params: map[string]any{}}
		}
		freeze := faults.Spec{Kind: faults.KindServerFreeze, Target: serverTarget(7), Params: map[string]any{}}
		apiOther := apiErrorSpec("conflict", 10)
		apiOther.Params["only"] = "read"
		cases := []struct {
			name          string
			first, second faults.Spec
			secondProject string
			conflict      bool
		}{
			{"same_kind_same_target", crash(7), crash(7), "p", true},
			{"second_api_error", apiErrorSpec("server_error", 50), apiOther, "p", true},
			{"second_rate_limit", rateLimitSpec(10), rateLimitSpec(20), "p", true},
			{"same_kind_other_target", crash(7), crash(8), "p", false},
			{"other_kind_same_target", crash(7), freeze, "p", false},
			{"same_kind_other_project", crash(7), crash(7), "q", false},
			{"api_error_other_project", apiErrorSpec("server_error", 50), apiErrorSpec("server_error", 50), "q", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := newEnv(t)
				first := e.create(t, "p", tc.first)
				_, err := e.M.Create(t.Context(), tc.secondProject, tc.second)
				if tc.conflict {
					if !errors.Is(err, faults.ErrConflict) {
						t.Errorf("error %v, want ErrConflict", err)
					}
					if got := e.listedIDs(""); !slices.Equal(got, []int64{first.ID}) {
						t.Errorf("listed %v, want only %d", got, first.ID)
					}
					return
				}
				if err != nil {
					t.Errorf("second Create: %v, want created", err)
				}
				if n := len(e.M.List("")); n != 2 {
					t.Errorf("%d faults listed, want 2", n)
				}
			})
		}
	})

	t.Run("list_and_end", func(t *testing.T) {
		e := newEnv(t)
		ctx := t.Context()
		e.create(t, "p", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(7), Params: map[string]any{}}) // 1
		e.create(t, "q", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(7), Params: map[string]any{}}) // 2
		e.create(t, "p", apiErrorSpec("server_error", 50))                                                             // 3
		e.create(t, "q", rateLimitSpec(10))                                                                            // 4
		e.create(t, "p", rateLimitSpec(10))                                                                            // 5

		wantIDs := func(project string, want ...int64) {
			t.Helper()
			if got := e.listedIDs(project); !slices.Equal(got, want) {
				t.Errorf("List(%q) = %v, want %v", project, got, want)
			}
			if project == "" {
				if n := e.M.Count(); n != len(want) {
					t.Errorf("Count() = %d, want %d", n, len(want))
				}
			}
		}
		for _, f := range e.M.List("p") {
			if f.Project != "p" {
				t.Errorf("List(p) gives fault %d of project %q", f.ID, f.Project)
			}
		}
		wantIDs("p", 1, 3, 5)
		wantIDs("q", 2, 4)
		wantIDs("", 1, 2, 3, 4, 5)

		if err := e.M.End(ctx, "p", 3); err != nil {
			t.Fatalf("End(p, 3): %v", err)
		}
		wantIDs("", 1, 2, 4, 5)
		if err := e.M.End(ctx, "p", 99); !errors.Is(err, faults.ErrNotFound) {
			t.Errorf("End(p, 99): %v, want ErrNotFound", err)
		}
		if err := e.M.End(ctx, "p", 2); !errors.Is(err, faults.ErrNotFound) {
			t.Errorf("End(p, 2) of project q: %v, want ErrNotFound", err)
		}
		if err := e.M.End(ctx, "p", 3); !errors.Is(err, faults.ErrNotFound) {
			t.Errorf("End(p, 3) twice: %v, want ErrNotFound", err)
		}
		wantIDs("", 1, 2, 4, 5)

		if err := e.M.EndProject(ctx, "p"); err != nil {
			t.Fatalf("EndProject(p): %v", err)
		}
		wantIDs("p")
		wantIDs("", 2, 4)

		e.create(t, "p", rateLimitSpec(10)) // 6
		if err := e.M.EndAll(ctx); err != nil {
			t.Fatalf("EndAll: %v", err)
		}
		wantIDs("")
	})
}

func testFault02(t *testing.T) {
	t.Run("duration", func(t *testing.T) {
		e := newEnv(t)
		timed := e.create(t, "p", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(7), Params: map[string]any{}, Duration: 5 * time.Minute})
		lasting := e.create(t, "p", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(8), Params: map[string]any{}})
		eff := e.Effects[faults.KindServerCrash]

		e.Clock.Advance(4 * time.Minute)
		if got := e.listedIDs("p"); !slices.Equal(got, []int64{timed.ID, lasting.ID}) {
			t.Fatalf("after 4 of 5 minutes listed %v, want %v", got, []int64{timed.ID, lasting.ID})
		}
		e.Clock.Advance(time.Minute)
		eventually(t, "the timed fault to leave the list", func() bool {
			return !slices.Contains(e.listedIDs("p"), timed.ID)
		})
		e.Clock.Advance(24 * time.Hour)
		if n := eff.UndoCount(timed.ID); n != 1 {
			t.Errorf("Undo ran %d times for the timed fault, want 1", n)
		}
		if got := e.listedIDs("p"); !slices.Equal(got, []int64{lasting.ID}) {
			t.Errorf("listed %v, want only the fault without duration %d", got, lasting.ID)
		}
		if n := eff.UndoCount(lasting.ID); n != 0 {
			t.Errorf("Undo ran %d times for the fault without duration, want 0", n)
		}
	})

	t.Run("target_deleted", func(t *testing.T) {
		e := newEnv(t)
		crash7 := e.create(t, "p", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(7), Params: map[string]any{}})
		net7 := e.create(t, "p", faults.Spec{Kind: faults.KindNetwork, Target: serverTarget(7), Params: map[string]any{"traffic": "all", "loss_percent": 100}})
		slow7 := e.create(t, "p", faults.Spec{Kind: faults.KindDiskSlow, Target: serverTarget(7), Params: map[string]any{"iops": 100}})
		crash8 := e.create(t, "p", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(8), Params: map[string]any{}})
		slowVol7 := e.create(t, "p", faults.Spec{Kind: faults.KindDiskSlow, Target: volumeTarget(7), Params: map[string]any{"iops": 100}})
		crashQ7 := e.create(t, "q", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(7), Params: map[string]any{}})
		api := e.create(t, "p", apiErrorSpec("server_error", 50))

		if err := e.M.EndTarget(t.Context(), "p", faults.TargetServer, 7); err != nil {
			t.Fatalf("EndTarget(p, server, 7): %v", err)
		}
		if got, want := e.listedIDs(""), []int64{crash8.ID, slowVol7.ID, crashQ7.ID, api.ID}; !slices.Equal(got, want) {
			t.Errorf("listed %v, want %v", got, want)
		}
		for _, c := range []struct {
			kind string
			id   int64
			want int
		}{
			{faults.KindServerCrash, crash7.ID, 1},
			{faults.KindNetwork, net7.ID, 1},
			{faults.KindDiskSlow, slow7.ID, 1},
			{faults.KindServerCrash, crash8.ID, 0},
			{faults.KindDiskSlow, slowVol7.ID, 0},
			{faults.KindServerCrash, crashQ7.ID, 0},
		} {
			if n := e.Effects[c.kind].UndoCount(c.id); n != c.want {
				t.Errorf("Undo of %s fault %d ran %d times, want %d", c.kind, c.id, n, c.want)
			}
		}
	})

	t.Run("undo", func(t *testing.T) {
		cases := []struct {
			name string
			end  func(e *env, t *testing.T, f faults.Fault) error
		}{
			{"end", func(e *env, t *testing.T, f faults.Fault) error { return e.M.End(t.Context(), f.Project, f.ID) }},
			{"end_project", func(e *env, t *testing.T, f faults.Fault) error { return e.M.EndProject(t.Context(), f.Project) }},
			{"end_all", func(e *env, t *testing.T, _ faults.Fault) error { return e.M.EndAll(t.Context()) }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := newEnv(t)
				eff := e.Effects[faults.KindServerCrash]
				listedAtUndo := make(chan bool, 1)
				eff.OnUndo = func(f faults.Fault) {
					select {
					case listedAtUndo <- listsDuringUndo(e, f.ID):
					default:
					}
				}
				f := e.create(t, "p", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(7), Params: map[string]any{}})
				if err := tc.end(e, t, f); err != nil {
					t.Fatalf("ending: %v", err)
				}
				select {
				case listed := <-listedAtUndo:
					if !listed {
						t.Errorf("fault %d was not listed while Undo ran", f.ID)
					}
				default:
					t.Fatalf("Undo did not run for fault %d", f.ID)
				}
				eff.OnUndo = nil
				if got := e.listedIDs(""); len(got) != 0 {
					t.Errorf("listed %v after ending, want none", got)
				}
				if n := eff.UndoCount(f.ID); n != 1 {
					t.Errorf("Undo ran %d times, want 1", n)
				}
			})
		}

		t.Run("apply_fails", func(t *testing.T) {
			e := newEnv(t)
			applyErr := errors.New("hyper-v refused")
			e.Effects[faults.KindServerCrash].ApplyErr = applyErr
			_, err := e.M.Create(t.Context(), "p", faults.Spec{Kind: faults.KindServerCrash, Target: serverTarget(7), Params: map[string]any{}})
			if !errors.Is(err, applyErr) {
				t.Errorf("Create: %v, want the Apply error", err)
			}
			if n := len(e.M.List("")); n != 0 {
				t.Errorf("%d faults listed, want 0", n)
			}
		})
	})
}

// listsDuringUndo reports whether List gives fault id, called from inside
// Undo. A List that blocks while Undo runs counts as not listed.
func listsDuringUndo(e *env, id int64) bool {
	got := make(chan bool, 1)
	go func() { got <- slices.Contains(e.listedIDs(""), id) }()
	select {
	case listed := <-got:
		return listed
	case <-time.After(stepTimeout):
		return false
	}
}

func withSeed(s faults.Spec, v uint64) faults.Spec {
	s.Seed = seed(v)
	return s
}

func fieldMatches(field string, want []string) bool {
	for _, w := range want {
		if field == w || strings.HasSuffix(field, "."+w) {
			return true
		}
	}
	return false
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(b)
}
