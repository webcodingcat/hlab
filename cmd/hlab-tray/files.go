package main

// Files opens the native file dialogs on the tray's window.
type Files struct{}

// PickISOFile lets the user choose one .iso file and returns its path; ""
// when the user cancels. It blocks until the dialog closes.
func (*Files) PickISOFile() (string, error) {
	tray.mu.Lock()
	app, win := tray.app, tray.window
	tray.mu.Unlock()
	return app.Dialog.OpenFile().
		SetTitle("Select ISO").
		AddFilter("ISO images", "*.iso").
		CanChooseFiles(true).
		AttachToWindow(win).
		PromptForSingleSelection()
}
