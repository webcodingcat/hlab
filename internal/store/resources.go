package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Meta is the indexed part of a stored resource. ProjectID 0 stands for a
// catalog resource without a project (project_id NULL).
type Meta struct {
	Kind      string
	ID        int64
	ProjectID int64
	Name      string
	Labels    map[string]string
	Created   time.Time
}

func nullProject(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// Put inserts or replaces the resource m.Kind/m.ID with doc stored as JSON.
// A zero m.Created is set to the current time.
func Put[T any](tx Tx, m Meta, doc T) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	labels := m.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	lb, err := json.Marshal(labels)
	if err != nil {
		return err
	}
	created := now()
	if !m.Created.IsZero() {
		created = formatTime(m.Created)
	}
	var name any
	if m.Name != "" {
		name = m.Name
	}
	_, err = tx.exec(`INSERT INTO resources(kind, id, project_id, name, labels, created, doc) VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(kind, id) DO UPDATE SET project_id = excluded.project_id, name = excluded.name,
		labels = excluded.labels, doc = excluded.doc`,
		m.Kind, m.ID, nullProject(m.ProjectID), name, string(lb), created, string(body))
	return err
}

// Get returns the resource kind/id of project projectID (0: no project), or
// ErrNotFound.
func Get[T any](tx Tx, kind string, projectID, id int64) (T, error) {
	var doc T
	var body string
	err := tx.tx.QueryRow(`SELECT doc FROM resources WHERE kind = ? AND id = ? AND project_id IS ?`,
		kind, id, nullProject(projectID)).Scan(&body)
	if err != nil {
		return doc, mapErr(err)
	}
	err = json.Unmarshal([]byte(body), &doc)
	return doc, err
}

// List returns all resources of kind in project projectID (0: no project),
// ordered by ID.
func List[T any](tx Tx, kind string, projectID int64) ([]T, error) {
	rows, err := tx.tx.Query(`SELECT doc FROM resources WHERE kind = ? AND project_id IS ? ORDER BY id`,
		kind, nullProject(projectID))
	if err != nil {
		return nil, err
	}
	return scanDocs[T](rows)
}

// ListAll returns all resources of kind in every project, ordered by ID.
// It is meant for hlabd itself (agent document, action work), never for a
// request of one project.
func ListAll[T any](tx Tx, kind string) ([]T, error) {
	rows, err := tx.tx.Query(`SELECT doc FROM resources WHERE kind = ? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	return scanDocs[T](rows)
}

func scanDocs[T any](rows *sql.Rows) ([]T, error) {
	defer func() { _ = rows.Close() }()
	out := []T{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var doc T
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

// NameTaken reports whether another resource of kind in project projectID
// (0: no project) than exceptID already has name.
func NameTaken(tx Tx, kind string, projectID int64, name string, exceptID int64) (bool, error) {
	var taken bool
	err := tx.tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM resources WHERE kind = ? AND project_id IS ? AND name = ? AND id != ?)`,
		kind, nullProject(projectID), name, exceptID).Scan(&taken)
	return taken, err
}

// IDs returns the IDs of all resources of kind in project projectID
// (0: no project), ordered.
func IDs(tx Tx, kind string, projectID int64) ([]int64, error) {
	rows, err := tx.tx.Query(`SELECT id FROM resources WHERE kind = ? AND project_id IS ? ORDER BY id`,
		kind, nullProject(projectID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Delete removes the resource kind/id of project projectID (0: no project),
// or returns ErrNotFound.
func Delete(tx Tx, kind string, projectID, id int64) error {
	return affected(tx.exec(`DELETE FROM resources WHERE kind = ? AND id = ? AND project_id IS ?`,
		kind, id, nullProject(projectID)))
}
