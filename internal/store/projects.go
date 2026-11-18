package store

import "time"

// Project is one project; resources and tokens belong to it.
type Project struct {
	ID      int64
	Name    string
	Created time.Time
}

// Token is an API token of a project. Only the SHA-256 of its value is stored.
type Token struct {
	ID        int64
	ProjectID int64
	Name      string
	ReadOnly  bool
	Created   time.Time
}

// CreateProject creates the project name; an existing name is ErrConflict.
func CreateProject(tx Tx, name string) (Project, error) {
	p := Project{Name: name, Created: time.Now().UTC()}
	res, err := tx.exec(`INSERT INTO projects(name, created) VALUES(?, ?)`, name, formatTime(p.Created))
	if err != nil {
		return Project{}, err
	}
	p.ID, err = res.LastInsertId()
	return p, err
}

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var created string
	if err := row.Scan(&p.ID, &p.Name, &created); err != nil {
		return Project{}, mapErr(err)
	}
	p.Created = parseTime(created)
	return p, nil
}

// ProjectByName returns the project name, or ErrNotFound.
func ProjectByName(tx Tx, name string) (Project, error) {
	return scanProject(tx.tx.QueryRow(`SELECT id, name, created FROM projects WHERE name = ?`, name))
}

// ListProjects returns all projects ordered by name.
func ListProjects(tx Tx) ([]Project, error) {
	rows, err := tx.tx.Query(`SELECT id, name, created FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProject removes the project id with its tokens and resources, or
// returns ErrNotFound.
func DeleteProject(tx Tx, id int64) error {
	return affected(tx.exec(`DELETE FROM projects WHERE id = ?`, id))
}

// CreateToken stores a token of project projectID by the SHA-256 of its
// value. A name already used in the project or a known hash is ErrConflict.
func CreateToken(tx Tx, projectID int64, name string, sha256 []byte, readOnly bool) error {
	_, err := tx.exec(`INSERT INTO tokens(project_id, name, sha256, read_only, created) VALUES(?, ?, ?, ?, ?)`,
		projectID, name, sha256, readOnly, now())
	return err
}

const tokenCols = `t.id, t.project_id, t.name, t.read_only, t.created`

func scanToken(row interface{ Scan(...any) error }, extra ...any) (Token, error) {
	var t Token
	var created string
	if err := row.Scan(append([]any{&t.ID, &t.ProjectID, &t.Name, &t.ReadOnly, &created}, extra...)...); err != nil {
		return Token{}, mapErr(err)
	}
	t.Created = parseTime(created)
	return t, nil
}

// ListTokens returns the tokens of project projectID ordered by name.
func ListTokens(tx Tx, projectID int64) ([]Token, error) {
	rows, err := tx.tx.Query(`SELECT `+tokenCols+` FROM tokens t WHERE t.project_id = ? ORDER BY t.name`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteToken removes the token name of project projectID, or returns
// ErrNotFound.
func DeleteToken(tx Tx, projectID int64, name string) error {
	return affected(tx.exec(`DELETE FROM tokens WHERE project_id = ? AND name = ?`, projectID, name))
}

// TokenByHash returns the token with the given SHA-256 and its project, or
// ErrNotFound.
func TokenByHash(tx Tx, sha256 []byte) (Token, Project, error) {
	var p Project
	var pcreated string
	row := tx.tx.QueryRow(`SELECT `+tokenCols+`, p.id, p.name, p.created
		FROM tokens t JOIN projects p ON p.id = t.project_id WHERE t.sha256 = ?`, sha256)
	t, err := scanToken(row, &p.ID, &p.Name, &pcreated)
	if err != nil {
		return Token{}, Project{}, err
	}
	p.Created = parseTime(pcreated)
	return t, p, nil
}
