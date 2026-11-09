package cloud_test

import (
	"fmt"
	"slices"
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
// back, and the appliance must then have pictures as the state is. The
// appliance's watchdog stays quiet (quietMachine): it would send the
// pictures itself, meet the failure in place of the steps, and later send
// what the undo forgot.

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

// quietMachine starts the lab's machine with a watchdog that does not
// check within the test.
func quietMachine(t *testing.T, test *testLab) *fake.Agent {
	t.Helper()
	return startMachineWatched(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024}, time.Hour)
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
	agent := quietMachine(t, test)
	agent.FailNext(agentproto.PictureMetadata)
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	expectFailed(t, test, created.Action)

	waitForPicture(t, agent, agentproto.PictureLinks, created.Server.PublicNet.IPv4.IP, false)
	waitForPicture(t, agent, agentproto.PictureDHCP, "# server "+fmt.Sprint(created.Server.ID), false)
}

func TestFailedPrimaryIPAssignAndUnassignLeaveThePicturesAsBefore(t *testing.T) {
	test := newTestLab(t)
	agent := quietMachine(t, test)
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

func TestFailedFloatingIPAssignLeavesThePicturesAsBefore(t *testing.T) {
	test := newTestLab(t)
	agent := quietMachine(t, test)
	first := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	second := createServer(t, test, map[string]any{"name": "db", "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	waitActions(t, test, first.Action, second.Action)
	floating := createFloatingIP(t, test, map[string]any{"type": "ipv4", "server": first.Server.ID})
	waitActions(t, test, *floating.Action)
	toFirst := `"gateway":"` + first.Server.PublicNet.IPv4.IP + `"`
	waitForPicture(t, agent, agentproto.PictureLinks, toFirst, true)

	agent.FailNext(agentproto.PictureMetadata)
	var moved schema.ActionGetResponse
	floatingIPAction(test, floating.FloatingIP.ID, "assign", map[string]any{"server": second.Server.ID}).Decode(t, &moved)
	expectFailed(t, test, moved.Action)
	if got := getFloatingIP(t, test, floating.FloatingIP.ID); got.Server == nil || *got.Server != first.Server.ID {
		t.Errorf("after the failed move the IP is %+v", got)
	}
	waitForPicture(t, agent, agentproto.PictureLinks, toFirst, true)
	waitForPicture(t, agent, agentproto.PictureLinks, `"gateway":"`+second.Server.PublicNet.IPv4.IP+`"`, false)
}

// failQueued makes the first action's steps fail while a second waits in
// the resource's queue behind it: the first holds at the links picture,
// the second is sent meanwhile, and the first then fails at the metadata.
func failQueued(t *testing.T, test *testLab, agent *fake.Agent, first, second func() schema.Action) {
	t.Helper()
	waiting, release := agent.HoldNext(agentproto.PictureLinks)
	failed := first()
	<-waiting
	agent.FailNext(agentproto.PictureMetadata)
	queued := second()
	release()
	expectFailed(t, test, failed)
	if ended := waitActions(t, test, queued); ended[0].Status != "success" {
		t.Fatalf("the queued %s ended as %s", queued.Command, ended[0].Status)
	}
}

// A failed delete_subnet does not bring its subnet back when an add_subnet
// of the same range was queued behind it: the network would have it twice.
func TestFailedDeleteSubnetKeepsTheAddQueuedBehind(t *testing.T) {
	test := newTestLab(t)
	agent := quietMachine(t, test)
	network := createNetwork(t, test, map[string]any{"name": "net", "ip_range": "10.0.0.0/16",
		"subnets": []map[string]any{cloudSubnet("10.0.1.0/24"), cloudSubnet("10.0.2.0/24")}})
	waitForPicture(t, agent, agentproto.PictureDHCP, fmt.Sprint("private network ", network.ID), true)
	failQueued(t, test, agent, func() schema.Action {
		return actionOf(t, "delete_subnet", networkAction(test, network.ID, "delete_subnet", map[string]any{"ip_range": "10.0.2.0/24"}))
	}, func() schema.Action {
		return actionOf(t, "add_subnet", networkAction(test, network.ID, "add_subnet", map[string]any{"type": "cloud", "network_zone": "eu-central", "ip_range": "10.0.2.0/24"}))
	})
	var ranges []string
	for _, subnet := range getNetwork(t, test, network.ID).Subnets {
		ranges = append(ranges, subnet.IPRange)
	}
	if !slices.Equal(ranges, []string{"10.0.1.0/24", "10.0.2.0/24"}) {
		t.Errorf("the subnets are %v, want 10.0.1.0/24 and 10.0.2.0/24 once each", ranges)
	}
}

// A failed detach does not bring its NIC back when an attach to the same
// network was queued behind it: the server would be in it twice.
func TestFailedDetachKeepsTheAttachQueuedBehind(t *testing.T) {
	test := newTestLab(t)
	agent := quietMachine(t, test)
	server := offServer(t, test, "web")
	network := networkWithSubnet(t, test, "net", "10.1")
	waitActions(t, test, actionOf(t, "attach", serverNetworkAction(test, server.Server.ID, "attach_to_network", map[string]any{"network": network})))
	failQueued(t, test, agent, func() schema.Action {
		return actionOf(t, "detach", serverNetworkAction(test, server.Server.ID, "detach_from_network", map[string]any{"network": network}))
	}, func() schema.Action {
		return actionOf(t, "attach", serverNetworkAction(test, server.Server.ID, "attach_to_network", map[string]any{"network": network}))
	})
	if got := getServer(t, test, server.Server.ID).PrivateNet; len(got) != 1 {
		t.Errorf("the server's private_net is %+v, want one NIC", got)
	}
}
