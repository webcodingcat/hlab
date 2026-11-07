// Package windows is the Windows platform: Hyper-V through a long-running
// PowerShell process, the host network, NRPT rules for the host's DNS and the
// process's life cycle. Its code builds for Windows only; for now every part
// reports platform.ErrNotImplemented (phase 1b brings Hyper-V).
package windows
