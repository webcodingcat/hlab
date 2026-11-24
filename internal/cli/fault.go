package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// faultTarget is the target of a fault object.
type faultTarget struct {
	Type string `json:"type"`
	ID   int64  `json:"id,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// faultRequest is the body of POST /projects/{name}/faults.
type faultRequest struct {
	Kind     string         `json:"kind"`
	Target   *faultTarget   `json:"target"`
	Params   map[string]any `json:"params"`
	Seed     *uint64        `json:"seed"`
	Duration string         `json:"duration,omitempty"`
}

// faultObject is a fault as the management API lists it.
type faultObject struct {
	ID      int64          `json:"id"`
	Project string         `json:"project"`
	Kind    string         `json:"kind"`
	Target  *faultTarget   `json:"target"`
	Params  map[string]any `json:"params"`
	Seed    *uint64        `json:"seed"`
	Created time.Time      `json:"created"`
	Ends    *time.Time     `json:"ends"`
}

// Flags of `fault create`.
var (
	faultTargetFlags = []string{"server", "volume", "storage-box", "from", "to"}
	faultIntParams   = []string{"loss-percent", "delay-ms", "jitter-ms", "percent", "per-hour", "iops"}
	faultTextParams  = []string{"traffic", "code", "only", "resource"}
	faultCreateOpts  = slices.Concat(faultTargetFlags, faultTextParams, faultIntParams, []string{"seed", "duration"})
)

// faultCreate is `hlab fault create`: the flags become a fault request.
func faultCreate(c *call) error {
	req := faultRequest{Kind: c.args[0], Params: map[string]any{}, Duration: c.flags["duration"]}
	for _, f := range []struct{ flag, typ string }{
		{"server", "server"}, {"volume", "volume"}, {"storage-box", "storage_box"},
	} {
		v := c.flags[f.flag]
		if v == "" {
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return usageError{fmt.Sprintf("--%s needs an integer ID, not %q", f.flag, v)}
		}
		if req.Target != nil {
			return usageError{"a fault takes one target"}
		}
		req.Target = &faultTarget{Type: f.typ, ID: id}
	}
	if c.flags["from"] != "" || c.flags["to"] != "" {
		if req.Target != nil {
			return usageError{"a fault takes one target"}
		}
		req.Target = &faultTarget{Type: "locations", From: c.flags["from"], To: c.flags["to"]}
	}
	for _, f := range faultTextParams {
		if v := c.flags[f]; v != "" {
			req.Params[strings.ReplaceAll(f, "-", "_")] = v
		}
	}
	for _, f := range faultIntParams {
		v := c.flags[f]
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return usageError{fmt.Sprintf("--%s needs an integer, not %q", f, v)}
		}
		req.Params[strings.ReplaceAll(f, "-", "_")] = n
	}
	if v := c.flags["seed"]; v != "" {
		seed, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return usageError{fmt.Sprintf("--seed needs an unsigned 64-bit integer, not %q", v)}
		}
		req.Seed = &seed
	}
	return submitFault(c, c.flags["project"], req)
}

// submitFault creates the fault req in project and prints it.
func submitFault(c *call, project string, req faultRequest) error {
	var raw []byte
	if err := c.mgmt(http.MethodPost, "/projects/"+pathEscape(project)+"/faults", req, &raw); err != nil {
		return err
	}
	var body struct {
		Fault faultObject `json:"fault"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	text := fmt.Sprintf("fault %d created", body.Fault.ID)
	if body.Fault.Seed != nil {
		text += fmt.Sprintf(" with seed %d", *body.Fault.Seed)
	}
	return c.out(json.RawMessage(raw), text+"\n")
}

// faultList is `hlab fault list [--project p]`.
func faultList(c *call) error {
	path := "/faults"
	if p := c.flags["project"]; p != "" {
		path = "/projects/" + pathEscape(p) + "/faults"
	}
	var raw []byte
	if err := c.mgmt(http.MethodGet, path, nil, &raw); err != nil {
		return err
	}
	var body struct {
		Faults []faultObject `json:"faults"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tPROJECT\tKIND\tTARGET\tPARAMS\tSEED\tREMAINING")
	for _, f := range body.Faults {
		seed, remaining := "-", "-"
		if f.Seed != nil {
			seed = strconv.FormatUint(*f.Seed, 10)
		}
		if f.Ends != nil {
			remaining = max(time.Until(*f.Ends), 0).Round(time.Second).String()
		}
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			f.ID, f.Project, f.Kind, targetText(f.Target), paramsText(f.Params), seed, remaining)
	}
	_ = tw.Flush()
	return c.out(json.RawMessage(raw), b.String())
}

func targetText(t *faultTarget) string {
	switch {
	case t == nil:
		return "-"
	case t.Type == "locations":
		return t.From + "->" + t.To
	default:
		return fmt.Sprintf("%s %d", t.Type, t.ID)
	}
}

func paramsText(p map[string]any) string {
	if len(p) == 0 {
		return "-"
	}
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(p)) {
		parts = append(parts, fmt.Sprintf("%s=%v", k, p[k]))
	}
	return strings.Join(parts, ",")
}

// faultDelete is `hlab fault delete`: `--project p <id>` ends one fault,
// `--project p --all` the project's, `--all` every fault.
func faultDelete(c *call) error {
	project, all := c.flags["project"], c.flags["all"] == "true"
	var path, text string
	switch {
	case len(c.args) == 1 && !all && project != "":
		id, err := strconv.ParseInt(c.args[0], 10, 64)
		if err != nil {
			return usageError{fmt.Sprintf("the fault ID must be an integer, not %q", c.args[0])}
		}
		path = "/projects/" + pathEscape(project) + "/faults/" + strconv.FormatInt(id, 10)
		text = fmt.Sprintf("fault %d ended\n", id)
	case len(c.args) == 0 && all && project != "":
		path = "/projects/" + pathEscape(project) + "/faults"
		text = "the faults of project " + project + " ended\n"
	case len(c.args) == 0 && all:
		path = "/faults"
		text = "all faults ended\n"
	default:
		return usageError{"hlab fault delete takes --project <name> <id>, --project <name> --all or --all"}
	}
	if err := c.mgmt(http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	return c.out(map[string]string{}, text)
}
