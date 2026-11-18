package store

import (
	"database/sql"
	"time"
)

// Action is one row of the actions table. Resources and Error hold JSON as
// written by the actions package; Error is "" while there is none. A zero
// Finished means the action has not finished.
type Action struct {
	ID        int64
	ProjectID int64
	Command   string
	Status    string
	Progress  int
	Started   time.Time
	Finished  time.Time
	Error     string
	Resources string
}

// CreateAction inserts a and returns its new ID. a.ID is ignored.
func CreateAction(tx Tx, a Action) (int64, error) {
	res, err := tx.exec(`INSERT INTO actions(project_id, command, status, progress, started, finished, error, resources)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		nullProject(a.ProjectID), a.Command, a.Status, a.Progress, formatTime(a.Started),
		nullTime(a.Finished), nullString(a.Error), a.Resources)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetAction returns the action id, or ErrNotFound.
func GetAction(tx Tx, id int64) (Action, error) {
	var a Action
	var project sql.NullInt64
	var started string
	var finished, errText sql.NullString
	err := tx.tx.QueryRow(`SELECT id, project_id, command, status, progress, started, finished, error, resources
		FROM actions WHERE id = ?`, id).Scan(&a.ID, &project, &a.Command, &a.Status, &a.Progress,
		&started, &finished, &errText, &a.Resources)
	if err != nil {
		return Action{}, mapErr(err)
	}
	a.ProjectID = project.Int64
	a.Started = parseTime(started)
	if finished.Valid {
		a.Finished = parseTime(finished.String)
	}
	a.Error = errText.String
	return a, nil
}

// UpdateAction writes status, progress, finished and error of a, or returns
// ErrNotFound.
func UpdateAction(tx Tx, a Action) error {
	return affected(tx.exec(`UPDATE actions SET status = ?, progress = ?, finished = ?, error = ? WHERE id = ?`,
		a.Status, a.Progress, nullTime(a.Finished), nullString(a.Error), a.ID))
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
