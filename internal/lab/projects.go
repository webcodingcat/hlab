package lab

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/webcodingcat/hlab/internal/state"
)

// Errors of the lab's own requests (projects, tokens, faults); the lab control
// answers them. A missing record is state.ErrNotFound.
var (
	ErrInvalid = errors.New("invalid")
	ErrExists  = errors.New("already exists")
)

// ErrProjectGone is CheckProject's error: the caller's project was deleted
// after its request was admitted. The API answers it as a deleted token.
var ErrProjectGone = errors.New("the project is gone")

// CheckProject makes sure that the project of a new record still exists,
// inside the Change that creates the record: a request admitted just before
// DeleteProject would otherwise leave a record of a gone project behind.
// Every create calls it; Act does so itself.
func CheckProject(tx *state.Tx, projectID int64) error {
	if _, err := state.Get(tx, state.Projects, projectID); err != nil {
		return fmt.Errorf("project %d: %w", projectID, ErrProjectGone)
	}
	return nil
}

// projectName is what a project may be called: a DNS label, because the name
// appears in URLs of the lab control and in file names of its logs.
var projectName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// CreateProject adds a project. Its servers reach the Internet, as the lab's
// default is.
func (l *Lab) CreateProject(name string, limits state.Limits) (state.Project, error) {
	if !projectName.MatchString(name) {
		return state.Project{}, fmt.Errorf("project name %q: %w: use up to 63 lowercase letters, digits and dashes", name, ErrInvalid)
	}
	if err := checkLimits(limits); err != nil {
		return state.Project{}, err
	}
	var project state.Project
	err := l.Change(func(tx *state.Tx) error {
		if _, err := projectByName(tx, name); err == nil {
			return fmt.Errorf("project %q: %w", name, ErrExists)
		}
		project = state.Project{
			ID:       state.NewID(tx, state.Projects),
			Name:     name,
			Created:  l.Now(),
			Limits:   limits,
			Internet: true,
		}
		state.Put(tx, state.Projects, &project)
		return nil
	})
	return project, err
}

// Projects returns every project, by ID.
func (l *Lab) Projects() []state.Project {
	var projects []state.Project
	_ = l.View(func(tx *state.Tx) error {
		for _, project := range state.All(tx, state.Projects) {
			projects = append(projects, *project)
		}
		return nil
	})
	return projects
}

// ProjectByName returns the project of that name, or state.ErrNotFound.
func (l *Lab) ProjectByName(name string) (state.Project, error) {
	var project state.Project
	err := l.View(func(tx *state.Tx) error {
		found, err := projectByName(tx, name)
		if err == nil {
			project = *found
		}
		return err
	})
	return project, err
}

// projectByName returns the project of that name, or state.ErrNotFound.
func projectByName(tx *state.Tx, name string) (*state.Project, error) {
	for _, project := range state.All(tx, state.Projects) {
		if project.Name == name {
			return project, nil
		}
	}
	return nil, fmt.Errorf("project %q: %w", name, state.ErrNotFound)
}

// SetProjectLimits replaces a project's limits; a limit of zero is no limit.
func (l *Lab) SetProjectLimits(projectID int64, limits state.Limits) error {
	if err := checkLimits(limits); err != nil {
		return err
	}
	return l.Change(func(tx *state.Tx) error {
		project, err := state.Get(tx, state.Projects, projectID)
		if err != nil {
			return err
		}
		project.Limits = limits
		return nil
	})
}

// checkLimits refuses negative limits: zero already means no limit.
func checkLimits(limits state.Limits) error {
	// a list, not a map, so that the first negative limit is named the same
	// on every call
	for _, limit := range []struct {
		name  string
		value int
	}{
		{"servers", limits.Servers},
		{"cores", limits.Cores},
		{"primary_ips", limits.PrimaryIPs},
		{"floating_ips", limits.FloatingIPs},
		{"volume_gb", limits.VolumeGB},
		{"networks", limits.Networks},
		{"load_balancers", limits.LoadBalancers},
	} {
		if limit.value < 0 {
			return fmt.Errorf("limit %s is %d: %w: use 0 for no limit", limit.name, limit.value, ErrInvalid)
		}
	}
	return nil
}

// DeleteProject removes a project with everything that belongs to it: its
// tokens, resources, actions and faults, and its servers' VMs, disks and
// seeds. Its running actions are canceled.
//
// The records go first, so that no new request of the project is admitted
// (its tokens are gone); then the running actions are canceled, and they find
// their actions gone when they end. A request admitted just before finds the
// project gone when it creates its record (CheckProject). Last, the servers
// go from the machine, as a server's delete does, now that no action of the
// project makes a VM any more. A server that the machine fails to remove
// is logged, and a closing lab removes none: the next start removes them
// as stray VMs.
func (l *Lab) DeleteProject(projectID int64) error {
	var servers []int64
	err := l.Change(func(tx *state.Tx) error {
		if _, err := state.Get(tx, state.Projects, projectID); err != nil {
			return err
		}
		servers = state.IDs(tx, state.Servers, func(server *state.Server) bool { return server.ProjectID == projectID })
		state.DeleteProject(tx, projectID)
		return nil
	})
	if err != nil {
		return err
	}
	// the removal is the lab's work like an action's, so that Close waits
	// for it; a closing lab leaves it to the next start
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.background.Add(1)
	l.mu.Unlock()
	defer l.background.Done()
	l.StopProjectActions(projectID)
	for _, id := range servers {
		if err := l.machine.deleteServerVM(l.ctx, id); err != nil {
			l.logger.Warn("removing a server of a deleted project; the next start removes what is left", "server", id, "error", err)
		}
	}
	return nil
}
