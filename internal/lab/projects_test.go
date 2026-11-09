package lab_test

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
)

func TestCreateProjectChecksTheName(t *testing.T) {
	l, _ := open(t, lab.Options{})
	project, err := l.CreateProject("web-1", state.Limits{Servers: 5})
	if err != nil {
		t.Fatal(err)
	}
	if project.ID < state.FirstID || !project.Internet || project.Limits.Servers != 5 {
		t.Errorf("the project is %+v", project)
	}
	if _, err := l.CreateProject("web-1", state.Limits{}); !errors.Is(err, lab.ErrExists) {
		t.Errorf("a second web-1: %v, want ErrExists", err)
	}
	for _, name := range []string{"", "Web", "web_1", "-web", "a/b"} {
		if _, err := l.CreateProject(name, state.Limits{}); !errors.Is(err, lab.ErrInvalid) {
			t.Errorf("name %q: %v, want ErrInvalid", name, err)
		}
	}
	if _, err := l.CreateProject("neg", state.Limits{Cores: -1}); !errors.Is(err, lab.ErrInvalid) {
		t.Errorf("a negative limit: %v, want ErrInvalid", err)
	}
}

func TestProjectsAreListedAndFoundByName(t *testing.T) {
	l, _ := open(t, lab.Options{})
	first, _ := l.CreateProject("first", state.Limits{})
	second, _ := l.CreateProject("second", state.Limits{})

	projects := l.Projects()
	if len(projects) != 2 || projects[0].ID != first.ID || projects[1].ID != second.ID {
		t.Errorf("the projects are %+v", projects)
	}
	if found, err := l.ProjectByName("second"); err != nil || found.ID != second.ID {
		t.Errorf("second is %+v, %v", found, err)
	}
	if _, err := l.ProjectByName("third"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("third: %v, want ErrNotFound", err)
	}
}

func TestSetProjectLimits(t *testing.T) {
	l, _ := open(t, lab.Options{})
	project, _ := l.CreateProject("limited", state.Limits{})
	if err := l.SetProjectLimits(project.ID, state.Limits{Servers: 2, VolumeGB: 100}); err != nil {
		t.Fatal(err)
	}
	if found, _ := l.ProjectByName("limited"); found.Limits.Servers != 2 || found.Limits.VolumeGB != 100 {
		t.Errorf("the limits are %+v", found.Limits)
	}
	if err := l.SetProjectLimits(project.ID+1, state.Limits{}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("another project: %v, want ErrNotFound", err)
	}
}

func TestDeleteProjectCancelsItsActionsAndRemovesItsRecords(t *testing.T) {
	l, _ := open(t, lab.Options{})
	project, _ := l.CreateProject("doomed", state.Limits{})
	token, _, err := l.CreateToken(project.ID, "ci", state.PermissionReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	action, err := l.Act(tableWith(blocking(release)), "poweroff", lab.Request{ProjectID: project.ID, ID: 7})
	if err != nil {
		t.Fatal(err)
	}

	if err := l.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}

	// the steps have ended: the server is no longer flagged, and a new action
	// of the gone project is refused for its project, not for a flag
	if _, err := l.Act(tableWith(instant), "poweroff", lab.Request{ProjectID: project.ID, ID: 7}); !errors.Is(err, lab.ErrProjectGone) {
		t.Errorf("a new action: %v, want ErrProjectGone", err)
	}
	for name, err := range map[string]error{
		"project": errOf(lab.Current(l, state.Projects, project.ID)),
		"token":   errOf(lab.Current(l, state.Tokens, token.ID)),
		"action":  errOf(lab.Current(l, state.Actions, action.ID)),
	} {
		if !errors.Is(err, state.ErrNotFound) {
			t.Errorf("the %s: %v, want ErrNotFound", name, err)
		}
	}
	if err := l.DeleteProject(project.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("deleting it again: %v, want ErrNotFound", err)
	}
}

// errOf returns the error of a call that returns a value too.
func errOf[T any](_ T, err error) error {
	return err
}

// A deleted project's servers leave the machine: their VMs, disks and
// seeds go, and the appliance gets pictures without them.
func TestDeleteProjectRemovesItsServersFromTheMachine(t *testing.T) {
	machine := fake.New()
	agent := withAppliance(machine)
	dataDir := t.TempDir()
	l := start(t, machine, lab.Options{DataDir: dataDir})
	t.Cleanup(func() { _ = l.Close() })
	id := putServer(t, l, machine, "web", true, true)
	if err := l.Machine().Start(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	server, err := lab.Current(l, state.Servers, id)
	if err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(dataDir, lab.ServerDiskDir, fmt.Sprint(id)+".vhdx")
	before := agent.Picture(agentproto.PictureLinks)

	if err := l.DeleteProject(server.ProjectID); err != nil {
		t.Fatal(err)
	}

	if state, err := machine.Platform().Hypervisor.VMState(t.Context(), lab.ServerVM(id)); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("the server's VM is %+v, %v; want it gone", state, err)
	}
	for _, file := range []string{disk, l.Machine().ServerSeed(id)} {
		if !slices.Contains(machine.Calls(), "DeleteDisk "+file) {
			t.Errorf("%s was not removed", file)
		}
	}
	if bytes.Equal(agent.Picture(agentproto.PictureLinks), before) {
		t.Error("the appliance got no new pictures")
	}
}

// A deleted project without servers still leaves the appliance's
// pictures: its networks go from them.
func TestDeleteProjectWithoutServersSendsThePictures(t *testing.T) {
	machine := fake.New()
	agent := withAppliance(machine)
	l := start(t, machine, lab.Options{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = l.Close() })
	putTestProject(t, l)
	err := l.Change(func(tx *state.Tx) error {
		state.Put(tx, state.Networks, &state.Network{
			ID: state.NewID(tx, state.Networks), ProjectID: projectID, Name: "net", IPRange: "10.0.0.0/16", VLAN: 2,
			Subnets: []state.NetworkSubnet{{Type: "cloud", IPRange: "10.0.1.0/24", NetworkZone: "eu-central"}},
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Machine().Start(t.Context(), machineOptions); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(agent.Picture(agentproto.PictureDHCP), []byte("private network")) {
		t.Fatalf("the network is not in the DHCP picture:\n%s", agent.Picture(agentproto.PictureDHCP))
	}

	if err := l.DeleteProject(projectID); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(agent.Picture(agentproto.PictureDHCP), []byte("private network")) {
		t.Errorf("the deleted project's network is still in the DHCP picture:\n%s", agent.Picture(agentproto.PictureDHCP))
	}
}
