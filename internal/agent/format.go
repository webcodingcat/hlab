//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// The bounds of a format: the disk shows up within a second of its
// attach; mkfs takes seconds, as it neither discards nor writes the
// inode tables of ext4 at once.
const (
	diskTimeout   = 10 * time.Second
	formatTimeout = 5 * time.Minute
)

// sysBlock is where the kernel lists the block devices.
const sysBlock = "/sys/block"

// format makes the file system of a Format on the disk with its WWN
// (agentproto.PathFormat).
func (a *Agent) format(w http.ResponseWriter, r *http.Request) {
	var wanted agentproto.Format
	if err := json.NewDecoder(r.Body).Decode(&wanted); err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Message: err.Error()})
		return
	}
	if err := wanted.Check(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, agentproto.Error{Message: err.Error()})
		return
	}
	started := time.Now()
	device, err := waitForDisk(r.Context(), sysBlock, wanted.WWN)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, agentproto.Error{Message: err.Error()})
		return
	}
	if err := makeFileSystem(r.Context(), device, wanted.FileSystem); err != nil {
		a.opts.Log.Error("format failed", "device", device, "file_system", wanted.FileSystem, "error", err)
		writeJSON(w, http.StatusInternalServerError, agentproto.Error{Message: err.Error()})
		return
	}
	a.opts.Log.Info("formatted", "device", device, "file_system", wanted.FileSystem, "took", time.Since(started))
	w.WriteHeader(http.StatusNoContent)
}

// waitForDisk returns the device of the disk with the WWN, waiting for it
// to appear below root (sysBlock): a disk's device/wwid is
// "naa.<the WWN's digits>".
func waitForDisk(ctx context.Context, root, wwn string) (string, error) {
	want := "naa." + strings.TrimPrefix(wwn, "0x")
	ctx, cancel := context.WithTimeout(ctx, diskTimeout)
	defer cancel()
	for {
		found, _ := filepath.Glob(filepath.Join(root, "*", "device", "wwid"))
		for _, path := range found {
			if wwid, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(wwid)) == want {
				return "/dev/" + filepath.Base(filepath.Dir(filepath.Dir(path))), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("no disk with the WWN %s", wwn)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// makeFileSystem makes a file system on a device, or a file in the tests,
// without discarding it first: the volume is new and empty anyway.
func makeFileSystem(ctx context.Context, device, fileSystem string) error {
	args := map[string][]string{
		agentproto.FileSystemExt4: {"mkfs.ext4", "-q", "-F", "-E", "nodiscard", device},
		agentproto.FileSystemXFS:  {"mkfs.xfs", "-q", "-f", "-K", device},
	}[fileSystem]
	if args == nil {
		return errors.New("unknown file system " + fileSystem)
	}
	ctx, cancel := context.WithTimeout(ctx, formatTimeout)
	defer cancel()
	if output, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return nil
}
