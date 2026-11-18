package store

import (
	"database/sql"
	"strings"
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

const actionColumns = `id, project_id, command, status, progress, started, finished, error, resources`

type scanner interface{ Scan(dest ...any) error }

func scanAction(row scanner) (Action, error) {
	var a Action
	var project sql.NullInt64
	var started string
	var finished, errText sql.NullString
	err := row.Scan(&a.ID, &project, &a.Command, &a.Status, &a.Progress,
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

// GetAction returns the action id, or ErrNotFound.
func GetAction(tx Tx, id int64) (Action, error) {
	return scanAction(tx.tx.QueryRow(`SELECT `+actionColumns+` FROM actions WHERE id = ?`, id))
}

// ListActions returns the actions of projectID with one of ids, ordered by
// ID. Missing IDs are skipped.
func ListActions(tx Tx, projectID int64, ids []int64) ([]Action, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{projectID}
	for _, id := range ids {
		args = append(args, id)
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	return queryActions(tx, `SELECT `+actionColumns+` FROM actions
		WHERE project_id = ? AND id IN (`+marks+`) ORDER BY id`, args...)
}

// RunningActions returns all actions with status "running", ordered by ID.
func RunningActions(tx Tx) ([]Action, error) {
	return queryActions(tx, `SELECT `+actionColumns+` FROM actions WHERE status = 'running' ORDER BY id`)
}

func queryActions(tx Tx, query string, args ...any) ([]Action, error) {
	rows, err := tx.tx.Query(query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = rows.Close() }()
	var out []Action
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
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
