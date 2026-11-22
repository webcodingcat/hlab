//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// testMCP05: an MCP client starts `bin\hlab.exe mcp` against the running
// dev service, creates a project and a token in it and deletes the
// project, each call with a deadline of its own.
func testMCP05(t *testing.T) {
	cmd := exec.Command(harness.hlab, "mcp")
	cmd.Dir = harness.root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	t.Cleanup(func() {
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("hlab mcp stderr:\n%s", stderr.String())
		}
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "hlab-e2e", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	cancel()
	if err != nil {
		t.Fatalf("connect to hlab mcp: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Logf("close the hlab mcp session: %v", err)
		}
	})

	displayName := harness.runID + "-mcp"
	text := callMCPTool(t, session, 2*time.Minute, "project_create", map[string]any{"display_name": displayName})
	var project struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &project); err != nil || project.Name == "" {
		t.Fatalf("project_create returned no project: %v\n%s", err, text)
	}
	deleted := false
	t.Cleanup(func() {
		if deleted {
			return
		}
		if err := deleteProject(project.Name); err != nil {
			t.Error(err)
		}
	})
	if !leftoverProject.MatchString(project.Name) {
		t.Errorf("project_create with display name %q made %q, which the harness does not clean up", displayName, project.Name)
	}

	text = callMCPTool(t, session, 2*time.Minute, "token_create", map[string]any{"project": project.Name, "name": "mcp-05"})
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(strings.NewReader(text)).Decode(&tok); err != nil || tok.Token == "" {
		t.Errorf("token_create returned no token value: %v", err)
	}

	callMCPTool(t, session, 10*time.Minute, "project_delete", map[string]any{"name": project.Name})
	deleted = true

	r, err := runHlab(context.Background(), 2*time.Minute, "project", "list", "--output", "json")
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("hlab project list: %v\n%s", err, r)
	}
	var list struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &list); err != nil {
		t.Fatalf("hlab project list --output json: %v\n%s", err, r.Stdout)
	}
	for _, p := range list.Projects {
		if p.Name == project.Name {
			t.Errorf("project %s is still listed after project_delete", project.Name)
		}
	}
}

// callMCPTool calls a tool with its own deadline and returns the text of
// its result; a protocol error or a tool error fails the test.
func callMCPTool(t *testing.T, session *mcp.ClientSession, timeout time.Duration, name string, args map[string]any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("MCP tool %s: %v", name, err)
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if res.IsError {
		t.Fatalf("MCP tool %s returned a tool error: %s", name, text)
	}
	return text
}
