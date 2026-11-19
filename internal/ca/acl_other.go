//go:build !windows

package ca

// restrictKey does nothing outside Windows; the key is written with mode
// 0600.
func restrictKey(string) error { return nil }
