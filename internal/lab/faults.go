package lab

import (
	"fmt"
	"net/http"

	"github.com/webcodingcat/hlab/internal/state"
)

// SetAPIFault makes the next fault.Count API calls of a project fail with
// fault.Status, each after fault.DelayMS; a Count of zero takes the fault
// away. The counter is in the state, so that a restart keeps it.
func (l *Lab) SetAPIFault(projectID int64, fault state.APIFault) error {
	if fault.Count < 0 || fault.DelayMS < 0 {
		return fmt.Errorf("API fault %+v: %w: count and delay must not be negative", fault, ErrInvalid)
	}
	if fault.Count > 0 && (fault.Status < http.StatusBadRequest || fault.Status > 599) {
		return fmt.Errorf("API fault status %d: %w: use an error status (400 to 599)", fault.Status, ErrInvalid)
	}
	if fault.Count == 0 {
		fault = state.APIFault{}
	}
	return l.Change(func(tx *state.Tx) error {
		if _, err := state.Get(tx, state.Projects, projectID); err != nil {
			return err
		}
		own := tx.Lab()
		if own.Faults == nil {
			own.Faults = map[int64]state.ProjectFaults{}
		}
		faults := own.Faults[projectID]
		faults.API = fault
		own.Faults[projectID] = faults
		return nil
	})
}

// TakeAPIFault counts one API call of a project against its fault: if calls
// are still to fail, it takes one off the counter and returns the fault.
func (l *Lab) TakeAPIFault(projectID int64) (state.APIFault, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// most calls find no fault: a read copies nothing
	if l.read().Lab().Faults[projectID].API.Count == 0 {
		return state.APIFault{}, false
	}
	var fault state.APIFault
	err := l.changeLocked(func(tx *state.Tx) error {
		faults := tx.Lab().Faults
		project := faults[projectID]
		fault = project.API
		project.API.Count--
		faults[projectID] = project
		return nil
	})
	if err != nil {
		// only encoding the state can fail here; the call then runs without
		// its fault rather than failing as a lab error
		l.logger.Error("taking an API fault failed; the call runs without it", "project", projectID, "error", err)
		return state.APIFault{}, false
	}
	return fault, true
}
