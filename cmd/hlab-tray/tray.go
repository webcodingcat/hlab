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

// Labels of the tray menu items.
const (
	labelOpen  = "Open hlab"
	labelStart = "Start cloud"
	labelStop  = "Stop cloud"
	labelQuit  = "Quit"
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
	start   *application.MenuItem
	stop    *application.MenuItem

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
		Title:     "hlab",
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
	item := func(label string) *application.MenuItem {
		return menu.Add(label).OnClick(func(*application.Context) {
			if err := t.click(label); err != nil && !errors.Is(err, errDisabled) {
				t.fail(err)
			}
		})
	}
	item(labelOpen)
	start := item(labelStart)
	stop := item(labelStop)
	menu.AddSeparator()
	item(labelQuit)

	systray := app.SystemTray.New()
	systray.SetMenu(menu)
	systray.OnClick(t.showWindow)

	t.mu.Lock()
	t.app, t.window, t.cloud = app, window, cloud
	t.systray, t.menu, t.status, t.start, t.stop = systray, menu, status, start, stop
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
	t.start.SetEnabled(m.CanStart)
	t.stop.SetEnabled(m.CanStop)
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

// click runs the action of the menu item with label; a disabled item
// returns errDisabled.
func (t *trayUI) click(label string) error {
	m := t.state()
	switch label {
	case labelOpen:
		t.showWindow()
		return nil
	case labelStart:
		if !m.CanStart {
			return errDisabled
		}
		return t.cloud.Start(false)
	case labelStop:
		if !m.CanStop {
			return errDisabled
		}
		return t.cloud.Stop()
	case labelQuit:
		t.quit()
		return nil
	}
	return fmt.Errorf("no menu item %q", label)
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

// quit ends the tray only; the cloud keeps running.
func (t *trayUI) quit() {
	t.quitting.Store(true)
	t.app.Quit()
}

// fail shows a failed menu action in a message box.
func (t *trayUI) fail(err error) {
	t.app.Dialog.Error().SetTitle("hlab").SetMessage(err.Error()).Show()
}
