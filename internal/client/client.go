// Package client talks to the lab control (/_lab) of a running hlabd: it finds
// the control token, sends the requests of package labapi over TCP and turns
// error answers into Go errors.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/labapi"
)

// ErrNotRunning means that no hlabd answers at the control address.
var ErrNotRunning = errors.New("HLab is not running")

// The environment variables that point the client elsewhere than the
// defaults, such as at `hlabd --platform fake` with its own data folder.
const (
	ControlVar   = "HLAB_CONTROL"    // the control's URL, such as http://127.0.0.1:8090
	TokenFileVar = "HLAB_TOKEN_FILE" // the control token's file
)

// Client is a connection to the lab control.
type Client struct {
	base  string // such as http://198.18.0.1:8090
	token string
	http  *http.Client
}

// New returns a client of the control at base, with the control token.
func New(base, token string) *Client {
	return &Client{
		base:  strings.TrimSuffix(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

// FromEnvironment returns a client of the control that the environment names
// (HLAB_CONTROL, else the default URL), with the token from the token file
// (FindTokenFile).
func FromEnvironment(defaultURL string) (*Client, error) {
	base := os.Getenv(ControlVar)
	if base == "" {
		base = defaultURL
	}
	path := FindTokenFile()
	token, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: no control token at %s (set %s if the data folder is elsewhere)", ErrNotRunning, path, TokenFileVar)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the control token: %w", err)
	}
	return New(base, strings.TrimSpace(string(token))), nil
}

// FindTokenFile returns where the control token is: HLAB_TOKEN_FILE, else
// the default data folder of this system. In WSL2 that is the folder of the
// HLab that runs on Windows, through /mnt/c.
func FindTokenFile() string {
	if path := os.Getenv(TokenFileVar); path != "" {
		return path
	}
	switch {
	case runtime.GOOS == "windows":
		return labapi.WindowsDataDir + `\` + labapi.TokenFile
	case inWSL():
		return labapi.WSLDataDir + "/" + labapi.TokenFile
	default:
		return labapi.LinuxDataDir + "/" + labapi.TokenFile
	}
}

// inWSL tells whether this Linux runs in WSL, whose interop sets
// WSL_DISTRO_NAME in every process.
func inWSL() bool {
	return os.Getenv("WSL_DISTRO_NAME") != ""
}

// Status returns the lab's status.
func (c *Client) Status() (labapi.Status, error) {
	var status labapi.Status
	err := c.call(http.MethodGet, "/status", nil, &status)
	return status, err
}

// Shutdown asks the lab to stop; it answers before it stops.
func (c *Client) Shutdown() error {
	return c.call(http.MethodPost, "/shutdown", nil, nil)
}

// Projects returns every project.
func (c *Client) Projects() ([]labapi.Project, error) {
	var projects []labapi.Project
	err := c.call(http.MethodGet, "/projects", nil, &projects)
	return projects, err
}

// CreateProject adds a project.
func (c *Client) CreateProject(name string) (labapi.Project, error) {
	var project labapi.Project
	err := c.call(http.MethodPost, "/projects", labapi.ProjectRequest{Name: name}, &project)
	return project, err
}

// DeleteProject removes a project with everything in it.
func (c *Client) DeleteProject(name string) error {
	return c.call(http.MethodDelete, "/projects/"+url.PathEscape(name), nil, nil)
}

// Tokens returns a project's tokens.
func (c *Client) Tokens(project string) ([]labapi.Token, error) {
	var tokens []labapi.Token
	err := c.call(http.MethodGet, "/projects/"+url.PathEscape(project)+"/tokens", nil, &tokens)
	return tokens, err
}

// EnvToken returns the project's env token, the same on every call.
func (c *Client) EnvToken(project string) (string, error) {
	var answer labapi.EnvToken
	err := c.call(http.MethodPost, "/projects/"+url.PathEscape(project)+"/env-token", nil, &answer)
	return answer.Secret, err
}

// CreateToken adds a token to a project and returns it with the token itself.
func (c *Client) CreateToken(project, name, permission string) (labapi.CreatedToken, error) {
	request := labapi.TokenRequest{Name: name, Permission: permission}
	var created labapi.CreatedToken
	err := c.call(http.MethodPost, "/projects/"+url.PathEscape(project)+"/tokens", request, &created)
	return created, err
}

// DeleteToken removes a project's token by its name.
func (c *Client) DeleteToken(project, name string) error {
	return c.call(http.MethodDelete, "/projects/"+url.PathEscape(project)+"/tokens/"+url.PathEscape(name), nil, nil)
}

// call sends a request below /_lab and decodes the answer into answer, if
// given. An error answer becomes an error with the lab's message; no answer
// at all is ErrNotRunning.
func (c *Client) call(method, path string, body, answer any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, c.base+labapi.Prefix+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%w (no answer at %s: %w)", ErrNotRunning, c.base, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode >= http.StatusBadRequest {
		var failure labapi.Error
		if json.Unmarshal(data, &failure) != nil || failure.Error.Message == "" {
			return fmt.Errorf("the lab control answered %s", response.Status)
		}
		return errors.New(failure.Error.Message)
	}
	if answer == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, answer)
}
