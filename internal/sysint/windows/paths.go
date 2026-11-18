//go:build windows

// Package windows implements the system integration for Windows: paths,
// the service through the SCM, the management pipe and the installer.
package windows

import (
	"os"
	"path/filepath"

	"github.com/webcodingcat/hlab/internal/profile"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// DataDir is %ProgramData%\<profile data folder>, the folder of
// settings.toml and state.db.
func DataDir() string {
	return filepath.Join(programData(), profile.Current().DataDirName)
}

// Paths returns the folders of the current profile. Release: data below
// %ProgramData%\hlab, programs and catalogs in %ProgramFiles%\hlab. Dev:
// data below %ProgramData%\hlab-dev, programs in the folder of the running
// executable (bin\ of the working copy) and catalogs in catalog\ next to it.
func Paths() (sysint.Paths, error) {
	data := DataDir()
	p := sysint.Paths{
		Config: data,
		State:  data,
		Logs:   filepath.Join(data, "logs"),
		CA:     filepath.Join(data, "ca"),
	}
	if profile.Current().Name == "release" {
		p.Programs = filepath.Join(programFiles(), "hlab")
		p.Catalog = filepath.Join(p.Programs, "catalog")
		return p, nil
	}
	exe, err := executable()
	if err != nil {
		return sysint.Paths{}, err
	}
	p.Programs = filepath.Dir(exe)
	p.Catalog = filepath.Join(p.Programs, "..", "catalog")
	return p, nil
}

func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func programData() string {
	if d := os.Getenv("ProgramData"); d != "" {
		return d
	}
	return `C:\ProgramData`
}

func programFiles() string {
	if d := os.Getenv("ProgramFiles"); d != "" {
		return d
	}
	return `C:\Program Files`
}
