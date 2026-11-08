package system

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ApplianceRelease is a release disk of the appliance in the data folder.
type ApplianceRelease struct {
	ID   string // the disk's SHA-256, hex
	Disk string // <folder>/<ID>.vhdx
}

// StageApplianceRelease copies the release disk that came with HLab into
// dir, named by its SHA-256, unless it is there already. The appliance runs
// on the copy, for two reasons: an update of HLab can replace its own disk
// while the old appliance runs on (Hyper-V holds a differencing disk's
// parent open), and both releases are there while the appliance's update
// runs the old and the new one side by side.
func (s *System) StageApplianceRelease(shipped, dir string) (ApplianceRelease, error) {
	id, err := fileSHA256(shipped)
	if err != nil {
		return ApplianceRelease{}, fmt.Errorf("reading the appliance's release disk: %w", err)
	}
	release := ApplianceRelease{ID: id, Disk: filepath.Join(dir, id+".vhdx")}
	if _, err := os.Stat(release.Disk); err == nil {
		return release, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ApplianceRelease{}, err
	}
	if err := copyFile(shipped, release.Disk); err != nil {
		return ApplianceRelease{}, fmt.Errorf("copying the appliance's release disk: %w", err)
	}
	return release, nil
}

// fileSHA256 returns the SHA-256 of a file, hex.
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// copyFile copies src to dst through a temporary file next to dst, so that
// dst is either whole or not there; a temporary file that a crash left
// goes with the next cleaning of the folder.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-")
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	err = errors.Join(err, out.Sync(), out.Close())
	if err == nil {
		err = os.Rename(out.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(out.Name())
	}
	return err
}
