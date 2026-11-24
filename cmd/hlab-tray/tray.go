package main

import (
	"embed"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/webcodingcat/hlab/internal/bridge"
)

// Labels of the fixed tray menu items; the cloud item's label comes from
// bridge.TrayModel.
const (
	labelOpen      = "Open HLab"
	labelEndFaults = "End all faults"
	labelQuit      = "Quit"
)

// errDisabled is returned for a click on a disabled menu item.
var errDisabled = errors.New("the menu item is disabled")

//go:embed icons/*.ico
var iconFiles embed.FS

// trayUI is the window, the tray icon and its menu.
type trayUI struct {
	quitting atomic.Bool // set before the application quits; the window closes then

	app     *application.App
	window  *application.WebviewWindow
	cloud   *bridge.Cloud
	systray *application.SystemTray
	menu    *application.Menu
	status  *application.MenuItem
	item    *application.MenuItem // the one cloud item: Start or Stop
	faults  *application.MenuItem // "End all faults", shown while faults are active

	updating sync.Mutex // one tray update at a time

	mu    sync.Mutex
	model bridge.TrayModel
}

// tray is the tray of this process.
var tray = &trayUI{}

// setup creates the window, shown at start and hidden on close, and the
// tray icon with its menu.
func (t *trayUI) setup(app *application.App, cloud *bridge.Cloud) {
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "HLab",
		URL:       "/",
		Width:     1200,
		Height:    760,
		MinWidth:  1000,
		MinHeight: 640,
	})
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !t.quitting.Load() {
			window.Hide()
			e.Cancel()
		}
	})

	menu := app.NewMenu()
	status := menu.Add(bridge.Tray(bridge.State{}).Status)
	status.SetEnabled(false)
	menu.AddSeparator()
	onClick := func(label func() string) func(*application.Context) {
		return func(*application.Context) {
			if err := t.click(label()); err != nil && !errors.Is(err, errDisabled) {
				t.fail(err)
			}
		}
	}
	fixed := func(label string) func() string { return func() string { return label } }
	menu.Add(labelOpen).OnClick(onClick(fixed(labelOpen)))
	item := menu.Add(bridge.Tray(bridge.State{}).CloudItem).
		OnClick(onClick(func() string { return t.state().CloudItem }))
	faults := menu.Add(labelEndFaults).OnClick(onClick(fixed(labelEndFaults)))
	faults.SetHidden(true)
	menu.AddSeparator()
	menu.Add(labelQuit).OnClick(onClick(fixed(labelQuit)))

	systray := app.SystemTray.New()
	systray.SetMenu(menu)
	systray.OnClick(t.showWindow)

	t.mu.Lock()
	t.app, t.window, t.cloud = app, window, cloud
	t.systray, t.menu, t.status, t.item, t.faults = systray, menu, status, item, faults
	t.mu.Unlock()
	t.apply(cloud.Status(), false)
}

// cloudChanged shows a new cloud state in the tray and sends it to the
// window as the "cloud" event.
func (t *trayUI) cloudChanged(s bridge.State) {
	t.apply(s, true)
}

// apply sets icon, tooltip and menu for s; emit also sends the "cloud"
// event.
func (t *trayUI) apply(s bridge.State, emit bool) {
	t.updating.Lock()
	defer t.updating.Unlock()
	m := bridge.Tray(s)
	t.mu.Lock()
	prev := t.model
	t.model = m
	t.mu.Unlock()

	if m.Icon != prev.Icon {
		if icon, err := iconFiles.ReadFile("icons/" + m.Icon + ".ico"); err == nil {
			t.systray.SetIcon(icon)
		}
	}
	t.systray.SetTooltip(m.Tooltip)
	t.status.SetLabel(m.Status)
	t.item.SetLabel(m.CloudItem)
	t.item.SetEnabled(m.CloudAction != "")
	t.faults.SetHidden(m.Faults == 0)
	t.menu.Update()
	if emit {
		t.app.Event.Emit("cloud", s)
	}
}

// emitLogs sends a batch of log lines to the window as the "logs" event.
func (t *trayUI) emitLogs(lines []string) {
	t.app.Event.Emit("logs", lines)
}

// state is what the tray shows now.
func (t *trayUI) state() bridge.TrayModel {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.model
}

// click runs the action of the menu item with label; for the cloud item
// label is its current label. A disabled item, and "End all faults" while
// it is hidden, returns errDisabled.
func (t *trayUI) click(label string) error {
	switch label {
	case labelOpen:
		t.showWindow()
		return nil
	case labelQuit:
		t.quit()
		return nil
	case labelEndFaults:
		if t.state().Faults == 0 {
			return errDisabled
		}
		return t.cloud.EndAllFaults()
	}
	m := t.state()
	if label != m.CloudItem {
		return fmt.Errorf("no menu item %q", label)
	}
	switch m.CloudAction {
	case "start":
		return t.cloud.Start(false)
	case "stop":
		return t.cloud.Stop()
	}
	return errDisabled
}

// showWindow shows, restores and focuses the window.
func (t *trayUI) showWindow() {
	t.mu.Lock()
	window := t.window
	t.mu.Unlock()
	if window == nil {
		return
	}
	window.Show()
	window.Restore()
	window.Focus()
}

// quit ends the tray only and closes the console windows; the cloud keeps
// running.
func (t *trayUI) quit() {
	t.quitting.Store(true)
	consoleWindows.closeAll()
	t.app.Quit()
}

// fail shows a failed menu action in a message box.
func (t *trayUI) fail(err error) {
	t.app.Dialog.Error().SetTitle("HLab").SetMessage(err.Error()).Show()
}
