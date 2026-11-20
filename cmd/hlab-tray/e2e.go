//go:build e2e

package main

import (
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/webcodingcat/hlab/internal/bridge"
)

// The e2e build runs next to the user's tray and opens its window to the
// test harness: its own single-instance ID, a remote debugging port on
// 127.0.0.1 and a per-run WebView user data folder from the harness's
// environment, and the TrayTest service. Release builds never carry the
// tag (DECISIONS.md "Tray UI tests through a CDP port in an e2e-only
// build").
func init() {
	e2eOptions = func(o *application.Options) {
		o.SingleInstance.UniqueID += ".e2e"
		if port := os.Getenv("HLAB_E2E_CDP_PORT"); port != "" {
			// WebView2 binds the debugging port to 127.0.0.1 (E9).
			o.Windows.AdditionalBrowserArgs = append(o.Windows.AdditionalBrowserArgs,
				"--remote-debugging-port="+port)
		}
		o.Windows.WebviewUserDataPath = os.Getenv("HLAB_E2E_WEBVIEW_DIR")
		o.Services = append(o.Services, application.NewService(&TrayTest{}))
	}
}

// TrayTest shows the tests what the tray shows and clicks its menu.
type TrayTest struct{}

// State is the icon key, tooltip, status line and the cloud item's label
// and action of the tray.
func (*TrayTest) State() bridge.TrayModel { return tray.state() }

// Click runs the menu item with the label item as a click in the menu
// would: "Open HLab", "Quit" or the cloud item's current label ("Start
// cloud", "Stop cloud"); a label the menu does not show is an error.
func (*TrayTest) Click(item string) error { return tray.click(item) }
