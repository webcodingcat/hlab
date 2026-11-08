package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/platform"
)

// agentTimeout bounds one request to the agent. The longest is a ruleset
// picture: up to 84 ms for 20 servers, measured on Hyper-V.
const agentTimeout = 10 * time.Second

// errRefused is a picture the agent refused (422): sending it again does not
// help.
var errRefused = errors.New("the agent refused the picture")

// agentClient talks HTTP to the agent of one VM, over the platform's dialer
// (hv_sock, vsock). It keeps its connection open between requests.
type agentClient struct {
	http *http.Client
}

// newAgentClient returns a client of the agent in the VM.
func newAgentClient(dialer platform.AgentDialer, vm string) agentClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.Dial(ctx, vm, agentproto.Port)
		},
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     time.Minute,
	}
	return agentClient{http: &http.Client{Transport: transport, Timeout: agentTimeout}}
}

// status asks the agent for its status; it is also the watchdog's ping.
func (c agentClient) status(ctx context.Context) (agentproto.Status, error) {
	var status agentproto.Status
	body, err := c.do(ctx, http.MethodGet, agentproto.PathStatus, nil, http.StatusOK)
	if err != nil {
		return status, err
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return status, fmt.Errorf("the agent's status: %w", err)
	}
	return status, nil
}

// put sends a picture.
func (c agentClient) put(ctx context.Context, name string, picture any) error {
	body, err := json.Marshal(picture)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPut, agentproto.PathPictures+name, body, http.StatusNoContent)
	return err
}

// do sends a request and returns the answer's body if its status is want;
// otherwise it fails with the agent's message.
func (c agentClient) do(ctx context.Context, method, path string, body []byte, want int) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == want {
		return answer, nil
	}
	var message agentproto.Error
	if json.Unmarshal(answer, &message) != nil || message.Message == "" {
		message.Message = string(bytes.TrimSpace(answer))
	}
	failure := fmt.Errorf("%s %s: %s: %s", method, path, response.Status, message.Message)
	if response.StatusCode == http.StatusUnprocessableEntity {
		return nil, fmt.Errorf("%w: %w", errRefused, failure)
	}
	return nil, failure
}

// close drops the open connection.
func (c agentClient) close() {
	c.http.CloseIdleConnections()
}
