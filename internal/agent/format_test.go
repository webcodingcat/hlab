//go:build linux

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// The appliance finds a volume to format by its WWN, which the kernel
// shows as device/wwid (measured with Hyper-V: "naa.6002248…").
func TestWaitForDiskFindsTheDiskByItsWWN(t *testing.T) {
	root := t.TempDir()
	for name, wwid := range map[string]string{"sda": "naa.60022480e0557c23836128478a6b4488", "sdb": "naa.60022480484c41420100000001000005"} {
		dir := filepath.Join(root, name, "device")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "wwid"), []byte(wwid+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	device, err := waitForDisk(context.Background(), root, "0x60022480484c41420100000001000005")
	if err != nil || device != "/dev/sdb" {
		t.Errorf("waitForDisk = %q, %v; want /dev/sdb", device, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitForDisk(canceled, root, "0x60022480484c41420100000001000099"); err == nil {
		t.Error("waitForDisk found a disk that is not there")
	}
}

func TestMakeFileSystemMakesWhatBlkidSees(t *testing.T) {
	for _, fileSystem := range []string{agentproto.FileSystemExt4, agentproto.FileSystemXFS} {
		tool := map[string]string{agentproto.FileSystemExt4: "mkfs.ext4", agentproto.FileSystemXFS: "mkfs.xfs"}[fileSystem]
		if _, err := exec.LookPath(tool); err != nil {
			t.Logf("no %s here; the appliance has it", tool)
			continue
		}
		disk := filepath.Join(t.TempDir(), "disk")
		if err := os.WriteFile(disk, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(disk, 320<<20); err != nil {
			t.Fatal(err)
		}
		if err := makeFileSystem(context.Background(), disk, fileSystem); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("blkid", "-o", "value", "-s", "TYPE", disk).Output(); err == nil && string(output) != fileSystem+"\n" {
			t.Errorf("blkid sees %q, want %s", output, fileSystem)
		}
	}
}
