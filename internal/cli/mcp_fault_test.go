package cli_test

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/faults"
)

// withoutInstance decodes text and drops what differs between two equal
// faults in different projects: id, project, created and ends.
func withoutInstance(t *testing.T, what, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%s returned %q: %v", what, text, err)
	}
	var strip func(v any)
	strip = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["kind"]; ok {
				for _, k := range []string{"id", "project", "created", "ends"} {
					delete(x, k)
				}
			}
			for _, e := range x {
				strip(e)
			}
		case []any:
			for _, e := range x {
				strip(e)
			}
		}
	}
	strip(v)
	return v
}

func testFaultTools(t *testing.T) {
	t.Run("tools", func(t *testing.T) {
		h, _ := newFaultHlab(t)
		cs := h.mcpSession(t, fastWaits, nil, nil)
		jsonOf := func(t *testing.T, args ...string) string {
			t.Helper()
			return strings.TrimSpace(h.mustRun(t, append(args, "--output", "json")...))
		}
		sameCreate := func(t *testing.T, tool, cmd string) {
			t.Helper()
			if a, b := withoutInstance(t, "fault_create", tool), withoutInstance(t, "hlab fault create", cmd); !reflect.DeepEqual(a, b) {
				t.Errorf("fault_create returned %s, want the shape and values of hlab fault create --output json %s", tool, cmd)
			}
		}
		ids := func(path string) []int64 { return faultIDList(apiFaults(t, h, path)) }

		// api_error without target, with seed and duration.
		text := mustSucceed(t, "fault_create api_error", callTool(t, cs, "fault_create", map[string]any{
			"project": "p", "kind": faults.KindAPIError,
			"params": map[string]any{"code": "service_error", "percent": 50},
			"seed":   12345, "duration": "1h",
		}))
		apiErr := decodeFault(t, "fault_create api_error", text)
		cmd := jsonOf(t, "fault", "create", "--project", "q", faults.KindAPIError, "--code", "service_error", "--percent", "50",
			"--seed", "12345", "--duration", "1h")
		sameCreate(t, text, cmd)
		cliErr := decodeFault(t, "hlab fault create api_error", cmd)
		if listed := apiFaults(t, h, "/projects/p/faults"); len(listed) != 1 || !sameJSON(t, listed[0], apiErr) ||
			listed[0].Seed == nil || *listed[0].Seed != 12345 || listed[0].Ends == nil || listed[0].Target != nil {
			t.Errorf("after fault_create api_error the API lists %+v, want the returned fault %+v with seed 12345, an end and no target", listed, apiErr)
		}

		// A fault with a server target.
		text = mustSucceed(t, "fault_create server_crash", callTool(t, cs, "fault_create", map[string]any{
			"project": "p", "kind": faults.KindServerCrash, "target": map[string]any{"type": "server", "id": 7},
		}))
		crash := decodeFault(t, "fault_create server_crash", text)
		cmd = jsonOf(t, "fault", "create", "--project", "q", faults.KindServerCrash, "--server", "7")
		sameCreate(t, text, cmd)
		cliCrash := decodeFault(t, "hlab fault create server_crash", cmd)
		if listed := apiFaults(t, h, "/projects/p/faults"); len(listed) != 2 || !sameJSON(t, listed[1], crash) ||
			!reflect.DeepEqual(listed[1].Target, &faults.Target{Type: faults.TargetServer, ID: 7}) {
			t.Errorf("after fault_create server_crash the API lists %+v, want the returned fault %+v on server 7 second", listed, crash)
		}

		// fault_list with and without project.
		if got, want := strings.TrimSpace(mustSucceed(t, "fault_list p", callTool(t, cs, "fault_list", map[string]any{"project": "p"}))),
			jsonOf(t, "fault", "list", "--project", "p"); got != want {
			t.Errorf("fault_list of p returned %q, want the output of hlab fault list --project p --output json %q", got, want)
		}
		if got, want := strings.TrimSpace(mustSucceed(t, "fault_list", callTool(t, cs, "fault_list", nil))),
			jsonOf(t, "fault", "list"); got != want {
			t.Errorf("fault_list returned %q, want the output of hlab fault list --output json %q", got, want)
		}

		// fault_delete with id.
		got := strings.TrimSpace(mustSucceed(t, "fault_delete id", callTool(t, cs, "fault_delete", map[string]any{"project": "p", "id": crash.ID})))
		if want := jsonOf(t, "fault", "delete", "--project", "q", strconv.FormatInt(cliCrash.ID, 10)); got != want {
			t.Errorf("fault_delete with id returned %q, want the output of hlab fault delete --output json %q", got, want)
		}
		if got, want := ids("/faults"), []int64{apiErr.ID, cliErr.ID}; !slices.Equal(got, want) {
			t.Errorf("after fault_delete of %d the API lists %v, want %v", crash.ID, got, want)
		}

		// fault_delete with all and project.
		got = strings.TrimSpace(mustSucceed(t, "fault_delete all p", callTool(t, cs, "fault_delete", map[string]any{"project": "p", "all": true})))
		if got, want := ids("/faults"), []int64{cliErr.ID}; !slices.Equal(got, want) {
			t.Errorf("after fault_delete all of p the API lists %v, want %v", got, want)
		}
		createFault(t, h, "--project", "p", faults.KindServerCrash, "--server", "7")
		if want := jsonOf(t, "fault", "delete", "--project", "p", "--all"); got != want {
			t.Errorf("fault_delete with all and project returned %q, want the output of hlab fault delete --project p --all --output json %q", got, want)
		}

		// fault_delete with all alone.
		createFault(t, h, "--project", "p", faults.KindServerCrash, "--server", "7")
		got = strings.TrimSpace(mustSucceed(t, "fault_delete all", callTool(t, cs, "fault_delete", map[string]any{"all": true})))
		if got := ids("/faults"); len(got) != 0 {
			t.Errorf("after fault_delete all the API lists %v, want none", got)
		}
		createFault(t, h, "--project", "q", faults.KindServerCrash, "--server", "7")
		if want := jsonOf(t, "fault", "delete", "--all"); got != want {
			t.Errorf("fault_delete with all returned %q, want the output of hlab fault delete --all --output json %q", got, want)
		}
	})

	t.Run("arguments", func(t *testing.T) {
		h, mgr := newFaultHlab(t)
		cs := h.mcpSession(t, fastWaits, nil, nil)
		apiError := func(extra map[string]any) map[string]any {
			args := map[string]any{
				"project": "p", "kind": faults.KindAPIError,
				"params": map[string]any{"code": "server_error", "percent": 10},
			}
			for k, v := range extra {
				args[k] = v
			}
			return args
		}
		const chosen = 0 // the seed hlab chooses
		for _, c := range []struct {
			name string
			tool string
			args map[string]any
			// seed is the seed the created fault carries; chosen for one
			// in 1..MaxChosenSeed. Unused for a tool error.
			seed      uint64
			toolError bool
		}{
			{name: "target null", tool: "fault_create", args: apiError(map[string]any{"target": nil}), seed: chosen},
			{name: "target omitted", tool: "fault_create", args: apiError(nil), seed: chosen},
			{name: "seed null", tool: "fault_create", args: apiError(map[string]any{"seed": nil}), seed: chosen},
			{name: "seed omitted", tool: "fault_create", args: apiError(nil), seed: chosen},
			{name: "seed 2^53-1", tool: "fault_create", args: apiError(map[string]any{"seed": int64(faults.MaxChosenSeed)}), seed: faults.MaxChosenSeed},
			{name: "seed 2^53", tool: "fault_create", args: apiError(map[string]any{"seed": uint64(1) << 53}), toolError: true},
			{name: "delete without id or all", tool: "fault_delete", args: map[string]any{"project": "p"}, toolError: true},
			{name: "delete with id and all", tool: "fault_delete", args: map[string]any{"project": "p", "id": 1, "all": true}, toolError: true},
		} {
			t.Run(c.name, func(t *testing.T) {
				if err := mgr.EndAll(context.Background()); err != nil {
					t.Fatalf("end the faults of the last case: %v", err)
				}
				if c.tool == "fault_delete" {
					createFault(t, h, "--project", "p", faults.KindRateLimit, "--per-hour", "10")
				}
				before := mgr.Count()
				res := callTool(t, cs, c.tool, c.args)
				if c.toolError {
					if !res.IsError || strings.TrimSpace(firstText(res)) == "" {
						t.Errorf("%s with %v: IsError %v, text %q; want a tool error with a message", c.tool, c.args, res.IsError, firstText(res))
					}
					if n := mgr.Count(); n != before {
						t.Errorf("%s with %v changed the number of faults from %d to %d", c.tool, c.args, before, n)
					}
					return
				}
				f := decodeFault(t, c.tool, mustSucceed(t, c.tool, res))
				if f.Kind != faults.KindAPIError || f.Target != nil {
					t.Errorf("%s with %v returned kind %q, target %+v; want api_error without target", c.tool, c.args, f.Kind, f.Target)
				}
				switch {
				case f.Seed == nil:
					t.Errorf("%s with %v returned no seed", c.tool, c.args)
				case c.seed == chosen && (*f.Seed < 1 || *f.Seed > faults.MaxChosenSeed):
					t.Errorf("%s with %v returned seed %d, want one chosen in 1..2^53-1", c.tool, c.args, *f.Seed)
				case c.seed != chosen && *f.Seed != c.seed:
					t.Errorf("%s with %v returned seed %d, want %d", c.tool, c.args, *f.Seed, c.seed)
				}
				if listed := apiFaults(t, h, "/projects/p/faults"); len(listed) != 1 || !sameJSON(t, listed[0], f) {
					t.Errorf("after %s with %v the API lists %+v, want the returned fault %+v", c.tool, c.args, listed, f)
				}
			})
		}
	})
}
