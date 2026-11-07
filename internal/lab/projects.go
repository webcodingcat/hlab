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
			Created:  l.now(),
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
	for name, value := range map[string]int{
		"servers":        limits.Servers,
		"cores":          limits.Cores,
		"primary_ips":    limits.PrimaryIPs,
		"floating_ips":   limits.FloatingIPs,
		"volume_gb":      limits.VolumeGB,
		"networks":       limits.Networks,
		"load_balancers": limits.LoadBalancers,
	} {
		if value < 0 {
			return fmt.Errorf("limit %s is %d: %w: use 0 for no limit", name, value, ErrInvalid)
		}
	}
	return nil
}

// DeleteProject removes a project with everything that belongs to it: its
// tokens, resources, actions and faults. Its running actions are canceled.
//
// The records go first, so that no new action of the project can start (its
// tokens are gone); then the running ones are canceled, and they find their
// actions gone when they end.
func (l *Lab) DeleteProject(projectID int64) error {
	err := l.Change(func(tx *state.Tx) error {
		if _, err := state.Get(tx, state.Projects, projectID); err != nil {
			return err
		}
		state.DeleteProject(tx, projectID)
		return nil
	})
	if err != nil {
		return err
	}
	l.StopProjectActions(projectID)
	return nil
}
