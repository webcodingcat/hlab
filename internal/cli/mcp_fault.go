package cli

import (
	"context"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxSeed is the largest seed fault_create accepts: 2^53-1, the same limit
// as the seed hlab chooses (internal/cli does not import internal/faults).
const maxSeed = 1<<53 - 1

type faultCreateArgs struct {
	Project  string         `json:"project" jsonschema:"name of the project"`
	Kind     string         `json:"kind" jsonschema:"kind of the fault, for example api_error or rate_limit"`
	Target   *faultTarget   `json:"target,omitempty" jsonschema:"target of the fault: type server, volume or storage_box with id, or type locations with from and to"`
	Params   map[string]any `json:"params,omitempty" jsonschema:"parameters of the kind, for example code and percent of api_error"`
	Seed     *uint64        `json:"seed,omitempty" jsonschema:"seed of the random draws, at most 2^53-1; hlab chooses one when omitted"`
	Duration string         `json:"duration,omitempty" jsonschema:"lifetime as a Go duration such as 5m, from 1s to 24h; without one the fault lasts until deleted"`
}

type faultListArgs struct {
	Project string `json:"project,omitempty" jsonschema:"name of the project; all projects when omitted"`
}

type faultDeleteArgs struct {
	Project string `json:"project,omitempty" jsonschema:"name of the project"`
	ID      *int64 `json:"id,omitempty" jsonschema:"ID of the fault to end; needs project"`
	All     bool   `json:"all,omitempty" jsonschema:"end every fault of project, or every fault at all without project"`
}

// faultCreateSchema is the input schema of fault_create: the inferred one
// with the seed limited to 2^53-1 (the MCP library decodes numbers as
// floats, exact only up to there), without "default" keywords.
func faultCreateSchema() *jsonschema.Schema {
	s, err := jsonschema.For[faultCreateArgs](nil)
	if err != nil {
		panic(err)
	}
	s.Properties["seed"].Maximum = jsonschema.Ptr(float64(maxSeed))
	return s
}

// The fault tools of mcpTools.
var (
	faultCreateTool = tool("fault_create", "Create a fault in a project, like hlab fault create; returns the fault with its ID and seed.", faultCreateSchema(),
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, in faultCreateArgs) *mcp.CallToolResult {
			req := faultRequest{Kind: in.Kind, Target: in.Target, Params: in.Params, Seed: in.Seed, Duration: in.Duration}
			if req.Params == nil {
				req.Params = map[string]any{}
			}
			return s.command(ctx, func(c *call) error { return submitFault(c, in.Project, req) }, nil, nil)
		})
	faultDeleteTool = tool("fault_delete", "End faults, like hlab fault delete: one with project and id, a project's with project and all, every fault with all alone.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, in faultDeleteArgs) *mcp.CallToolResult {
			if (in.ID == nil) == !in.All {
				return errorResult(usageError{"fault_delete takes either id (with project) or all"})
			}
			flags := map[string]string{"project": in.Project}
			var args []string
			if in.All {
				flags["all"] = "true"
			} else {
				args = []string{strconv.FormatInt(*in.ID, 10)}
			}
			return s.command(ctx, faultDelete, args, flags)
		})
	faultListTool = tool("fault_list", "List the active faults of a project or of all projects, like hlab fault list.", nil,
		func(s *mcpServer, ctx context.Context, _ *mcp.CallToolRequest, in faultListArgs) *mcp.CallToolResult {
			return s.command(ctx, faultList, nil, map[string]string{"project": in.Project})
		})
)
