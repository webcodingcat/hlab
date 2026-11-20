//go:build !windows

package settings

// CopySecurity does nothing outside Windows; the service only runs there.
func CopySecurity(_, _ string) error { return nil }
