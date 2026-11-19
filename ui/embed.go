// Package ui holds the built tray frontend for embedding in hlab-tray.
package ui

import "embed"

// Dist is ui/frontend/dist as Vite builds it. A committed placeholder keeps
// the pattern valid on a fresh checkout without a frontend build.
//
//go:embed all:frontend/dist
var Dist embed.FS
