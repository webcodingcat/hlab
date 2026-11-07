package control

import (
	"fmt"
	"net/http"

	"github.com/webcodingcat/hlab/internal/labapi"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

func (c *Control) listProjects(*http.Request) (int, any, error) {
	projects := []labapi.Project{}
	for _, project := range c.opts.Lab.Projects() {
		projects = append(projects, projectJSON(project))
	}
	return http.StatusOK, projects, nil
}

func (c *Control) createProject(r *http.Request) (int, any, error) {
	var request labapi.ProjectRequest
	if err := web.DecodeJSON(&web.Request{Request: r}, &request); err != nil {
		return 0, nil, err
	}
	project, err := c.opts.Lab.CreateProject(request.Name, state.Limits{})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, projectJSON(project), nil
}

func (c *Control) deleteProject(r *http.Request) (int, any, error) {
	project, err := c.opts.Lab.ProjectByName(r.PathValue("project"))
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, c.opts.Lab.DeleteProject(project.ID)
}

func (c *Control) listTokens(r *http.Request) (int, any, error) {
	project, err := c.opts.Lab.ProjectByName(r.PathValue("project"))
	if err != nil {
		return 0, nil, err
	}
	found, err := c.opts.Lab.Tokens(project.ID)
	if err != nil {
		return 0, nil, err
	}
	tokens := []labapi.Token{}
	for _, token := range found {
		tokens = append(tokens, tokenJSON(token))
	}
	return http.StatusOK, tokens, nil
}

func (c *Control) createToken(r *http.Request) (int, any, error) {
	var request labapi.TokenRequest
	if err := web.DecodeJSON(&web.Request{Request: r}, &request); err != nil {
		return 0, nil, err
	}
	project, err := c.opts.Lab.ProjectByName(r.PathValue("project"))
	if err != nil {
		return 0, nil, err
	}
	token, secret, err := c.opts.Lab.CreateToken(project.ID, request.Name, state.Permission(request.Permission))
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, labapi.CreatedToken{Token: tokenJSON(token), Secret: secret}, nil
}

// envToken answers the project's env token, which the lab makes on the first
// call; it is a POST because that call creates a token.
func (c *Control) envToken(r *http.Request) (int, any, error) {
	project, err := c.opts.Lab.ProjectByName(r.PathValue("project"))
	if err != nil {
		return 0, nil, err
	}
	secret, err := c.opts.Lab.EnvToken(project.ID)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, labapi.EnvToken{Secret: secret}, nil
}

func (c *Control) deleteToken(r *http.Request) (int, any, error) {
	project, err := c.opts.Lab.ProjectByName(r.PathValue("project"))
	if err != nil {
		return 0, nil, err
	}
	token, err := c.tokenByName(project.ID, r.PathValue("token"))
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, c.opts.Lab.DeleteToken(token.ID)
}

// tokenByName returns a project's token of that name, or state.ErrNotFound:
// the CLI names tokens, which are unique per project.
func (c *Control) tokenByName(projectID int64, name string) (state.Token, error) {
	tokens, err := c.opts.Lab.Tokens(projectID)
	if err != nil {
		return state.Token{}, err
	}
	for _, token := range tokens {
		if token.Name == name {
			return token, nil
		}
	}
	return state.Token{}, fmt.Errorf("token %q: %w", name, state.ErrNotFound)
}

// cloud answers a request of the web UI to the Cloud API, on behalf of the
// project in the path and with read_write, as the project's owner would.
func (c *Control) cloud(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	project, err := c.opts.Lab.ProjectByName(name)
	if err != nil {
		web.WriteError(w, c.opts.Logger, answer(err))
		return
	}
	caller := web.Caller{ProjectID: project.ID, Permission: state.PermissionReadWrite}
	prefix := labapi.Prefix + "/projects/" + name + "/cloud"
	http.StripPrefix(prefix, c.opts.Cloud.ServeAs(caller)).ServeHTTP(w, r)
}

// projectJSON returns a project as the lab control shows it.
func projectJSON(project state.Project) labapi.Project {
	return labapi.Project{ID: project.ID, Name: project.Name, Created: project.Created, Internet: project.Internet}
}

// tokenJSON returns a token as the lab control shows it.
func tokenJSON(token state.Token) labapi.Token {
	return labapi.Token{
		ID:         token.ID,
		Name:       token.Name,
		Permission: string(token.Permission),
		Prefix:     token.Prefix,
		Created:    token.Created,
	}
}
