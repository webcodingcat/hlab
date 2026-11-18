package api_test

import (
	"net/http"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
)

func TestAPI(t *testing.T) {
	t.Run("API-09", testErrors)
	t.Run("API-14", testRouter)
	t.Run("API-05", testList)
	t.Run("API-02", func(t *testing.T) {
		env := apitest.New(t, apitest.ProbeModule())
		other := env.NewToken("q", false)

		rejected := []struct {
			name          string
			authorization string
		}{
			{"no token", ""},
			{"malformed", "Bearer not a token"},
			{"not bearer", "Basic " + env.Token},
			{"unknown", "Bearer " + apitest.RandomToken()},
		}
		for _, c := range rejected {
			resp := env.DoAuth(c.authorization, http.MethodGet, "/ssh_keys", nil)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s: status %d, want 401", c.name, resp.StatusCode)
			}
			if code := apitest.ErrorCode(t, resp); code != "unauthorized" {
				t.Errorf("%s: error code %q, want unauthorized", c.name, code)
			}
		}

		for project, token := range map[string]string{"p": env.Token, "q": other} {
			resp := env.DoAuth("Bearer "+token, http.MethodGet, "/ssh_keys", nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("token of %s: status %d, want 200", project, resp.StatusCode)
			}
			var body struct {
				SSHKeys []struct {
					Name string `json:"name"`
				} `json:"ssh_keys"`
			}
			apitest.Decode(t, resp, &body)
			if len(body.SSHKeys) != 1 || body.SSHKeys[0].Name != project {
				t.Errorf("token of %s selected %+v, want project %s", project, body.SSHKeys, project)
			}
		}
	})
}
