//go:build !e2e

package main

import (
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/bridge"
)

func TestTray(t *testing.T) {
	t.Run("UI-01", func(t *testing.T) {
		// The variables of the e2e harness must not open the default build.
		t.Setenv("HLAB_E2E_CDP_PORT", "9340")
		t.Setenv("HLAB_E2E_WEBVIEW_DIR", t.TempDir())

		opts := appOptions(
			bridge.NewService(bridge.Deps{}),
			bridge.NewCloud(bridge.CloudDeps{}),
			bridge.NewLogs(bridge.LogsDeps{}),
		)
		for _, arg := range opts.Windows.AdditionalBrowserArgs {
			if strings.Contains(arg, "remote-debugging") {
				t.Errorf("default build passes the browser argument %q", arg)
			}
		}
		if p := opts.Windows.WebviewUserDataPath; p != "" {
			t.Errorf("default build sets the WebView user data path %q, want none", p)
		}
	})
}
