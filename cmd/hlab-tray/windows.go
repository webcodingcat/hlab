package main

import (
	"net/url"
	"strconv"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// consoleKey names the console window of one server.
type consoleKey struct {
	project string
	id      int64
}

// Windows opens the tray's further windows: the server consoles. It keeps
// its own window map, because Close is asynchronous and GetByName returns
// the first match (E24).
type Windows struct {
	mu      sync.Mutex
	app     *application.App
	windows map[consoleKey]*application.WebviewWindow
}

// consoleWindows is the window service of this process.
var consoleWindows = &Windows{}

// setApp gives the service the application it creates windows in.
func (w *Windows) setApp(app *application.App) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.app = app
}

// OpenConsole creates the window "Console: <name>" at
// /#/server-console/<project>/<id>, or shows, restores and focuses the
// open one of that server.
func (w *Windows) OpenConsole(project string, id int64, name string) error {
	key := consoleKey{project, id}
	w.mu.Lock()
	win := w.windows[key]
	if win == nil {
		ids := strconv.FormatInt(id, 10)
		win = w.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:   "console-" + project + "-" + ids,
			Title:  "Console: " + name,
			URL:    "/#/server-console/" + url.PathEscape(project) + "/" + ids,
			Width:  1100,
			Height: 800,
		})
		created := win
		win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.windows[key] == created {
				delete(w.windows, key)
			}
		})
		if w.windows == nil {
			w.windows = map[consoleKey]*application.WebviewWindow{}
		}
		w.windows[key] = win
	}
	w.mu.Unlock()
	win.Show()
	win.Restore()
	win.Focus()
	return nil
}

// closeAll closes every open console window.
func (w *Windows) closeAll() {
	w.mu.Lock()
	open := make([]*application.WebviewWindow, 0, len(w.windows))
	for k, win := range w.windows {
		open = append(open, win)
		delete(w.windows, k)
	}
	w.mu.Unlock()
	for _, win := range open {
		win.Close()
	}
}
