//go:build !windows

package settings

// copySecurity does nothing outside Windows; the service only runs there.
func copySecurity(_, _ string) error { return nil }
