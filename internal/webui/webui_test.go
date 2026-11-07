package webui_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/webui"
)

const address = "127.0.0.1:8090"

// newGuarded returns the UI and its guard in front of a handler that answers
// 200 to everything that passes.
func newGuarded() (*webui.UI, http.Handler) {
	ui := webui.New([]string{address}, slog.New(slog.DiscardHandler))
	return ui, ui.Guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

// send sends a request with that Host and the headers to the handler and
// returns the status.
func send(handler http.Handler, method, host string, headers map[string]string) int {
	request := httptest.NewRequest(method, "/_lab/projects", nil)
	request.Host = host
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code
}

func TestGuardTakesOnlyTheLabsHostNames(t *testing.T) {
	_, guarded := newGuarded()
	for host, want := range map[string]int{
		"127.0.0.1:8090":         http.StatusOK,
		"localhost:8090":         http.StatusOK,
		"evil.example:8090":      http.StatusForbidden, // DNS rebinding
		"127.0.0.1:9999":         http.StatusForbidden,
		"localhost.example:8090": http.StatusForbidden,
	} {
		if got := send(guarded, http.MethodGet, host, nil); got != want {
			t.Errorf("Host %s: got %d, want %d", host, got, want)
		}
	}
}

func TestGuardTakesChangesOnlyFromThePageOrWithAToken(t *testing.T) {
	_, guarded := newGuarded()
	for name, test := range map[string]struct {
		headers map[string]string
		want    int
	}{
		"from the page":         {map[string]string{"Origin": "http://" + address}, http.StatusOK},
		"from another website":  {map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		"from the CLI":          {map[string]string{"Authorization": "Bearer token"}, http.StatusOK},
		"without either":        {nil, http.StatusForbidden},
		"another site, a token": {map[string]string{"Origin": "http://evil.example", "Authorization": "Bearer token"}, http.StatusForbidden},
	} {
		if got := send(guarded, http.MethodPost, address, test.headers); got != test.want {
			t.Errorf("%s: got %d, want %d", name, got, test.want)
		}
	}
	if got := send(guarded, http.MethodGet, address, map[string]string{"Origin": "http://evil.example"}); got != http.StatusOK {
		t.Errorf("a read from another website: got %d, want 200 (the browser keeps the answer from it)", got)
	}
}

func TestThePageBringsTheCookieThatAdmitsIt(t *testing.T) {
	ui, _ := newGuarded()
	recorder := httptest.NewRecorder()
	ui.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "<title>HLab</title>") {
		t.Fatalf("got %d %q, want the page", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != webui.CookieName || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("got the cookies %v, want one HttpOnly, SameSite=Strict %s", cookies, webui.CookieName)
	}

	withCookie := httptest.NewRequest(http.MethodGet, "/_lab/status", nil)
	withCookie.AddCookie(cookies[0])
	if !ui.Admits(withCookie) {
		t.Error("the page's cookie is not admitted")
	}
	forged := httptest.NewRequest(http.MethodGet, "/_lab/status", nil)
	forged.AddCookie(&http.Cookie{Name: webui.CookieName, Value: "guessed"})
	if ui.Admits(forged) || ui.Admits(httptest.NewRequest(http.MethodGet, "/_lab/status", nil)) {
		t.Error("a request without the page's cookie is admitted")
	}
}

func TestThePageServesItsScriptAndStyle(t *testing.T) {
	ui, _ := newGuarded()
	for _, path := range []string{"/ui.js", "/ui.css"} {
		recorder := httptest.NewRecorder()
		ui.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", path, recorder.Code)
		}
	}
}
