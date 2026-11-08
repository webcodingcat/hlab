package cloud_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
)

// The tests in this file make an action's steps fail after the appliance
// took the first pictures with the quick changes: the agent fails the
// metadata picture, which goes last. The action's undo takes the changes
// back, and the appliance must then have pictures as the state is.

// waitForPicture waits until the agent's picture of a name has (or lacks,
// with has false) a text.
func waitForPicture(t *testing.T, agent *fake.Agent, name, text string, has bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for strings.Contains(string(agent.Picture(name)), text) != has {
		if time.Now().After(deadline) {
			t.Fatalf("the %s picture still %s %q:\n%s", name, map[bool]string{true: "lacks", false: "has"}[has], text, agent.Picture(name))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// expectFailed fails the test unless the action ended with an error.
func expectFailed(t *testing.T, test *testLab, action schema.Action) {
	t.Helper()
	if ended := waitActions(t, test, action); ended[0].Status != "error" {
		t.Fatalf("%s ended as %s, want error", action.Command, ended[0].Status)
	}
}

func TestFailedServerCreateLeavesNoTraceInThePictures(t *testing.T) {
	test := newTestLab(t)
	agent := startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	agent.FailNext(agentproto.PictureMetadata)
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	expectFailed(t, test, created.Action)

	waitForPicture(t, agent, agentproto.PictureLinks, created.Server.PublicNet.IPv4.IP, false)
	waitForPicture(t, agent, agentproto.PictureDHCP, "# server "+fmt.Sprint(created.Server.ID), false)
}

func TestFailedPrimaryIPAssignAndUnassignLeaveThePicturesAsBefore(t *testing.T) {
	test := newTestLab(t)
	agent := startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	server := createServer(t, test, map[string]any{
		"name": "web", "server_type": "cpx22", "image": "debian-13", "start_after_create": false,
		"public_net": map[string]any{"enable_ipv4": false},
	})
	waitActions(t, test, server.Action)
	ip := createPrimaryIP(t, test, map[string]any{"name": "spare", "type": "ipv4", "location": "fsn1", "assignee_type": "server"}).PrimaryIP

	agent.FailNext(agentproto.PictureMetadata)
	assign := primaryIPAction(test, ip.ID, "assign", map[string]any{"assignee_id": server.Server.ID, "assignee_type": "server"})
	var assigned schema.ActionGetResponse
	assign.Decode(t, &assigned)
	expectFailed(t, test, assigned.Action)
	if got := getPrimaryIP(t, test, ip.ID); got.AssigneeID != nil {
		t.Errorf("after the failed assign the IP is assigned to %d", *got.AssigneeID)
	}
	waitForPicture(t, agent, agentproto.PictureLinks, `"`+ip.IP+`/32"`, false)

	assign = primaryIPAction(test, ip.ID, "assign", map[string]any{"assignee_id": server.Server.ID, "assignee_type": "server"})
	assign.Decode(t, &assigned)
	waitActions(t, test, assigned.Action)
	waitForPicture(t, agent, agentproto.PictureLinks, `"`+ip.IP+`/32"`, true)

	agent.FailNext(agentproto.PictureMetadata)
	var unassigned schema.ActionGetResponse
	primaryIPAction(test, ip.ID, "unassign", nil).Decode(t, &unassigned)
	expectFailed(t, test, unassigned.Action)
	if got := getPrimaryIP(t, test, ip.ID); got.AssigneeID == nil || *got.AssigneeID != server.Server.ID {
		t.Errorf("after the failed unassign the IP is %+v", got)
	}
	if got := getServer(t, test, server.Server.ID); got.PublicNet.IPv4 == nil || got.PublicNet.IPv4.ID != ip.ID {
		t.Errorf("after the failed unassign the server's IPv4 is %+v", got.PublicNet.IPv4)
	}
	// the pictures with the unassign went out, and the undo's put the
	// route back
	waitForPicture(t, agent, agentproto.PictureLinks, `"`+ip.IP+`/32"`, true)
}
