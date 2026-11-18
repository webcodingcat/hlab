package agent_test

import (
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/agent"
)

// Identifiers of the Debian image before and after rescue-prepare.
const (
	oldRootPart = "7b3c1f0e-55aa-4c2d-9e61-0a1b2c3d4e5f"
	oldESPPart  = "c0ffee00-1234-4abc-8def-001122334455"
	oldRootFS   = "e1d2c3b4-a5f6-4789-8abc-def012345678"
	newRootPart = "11111111-2222-4333-8444-555555555555"
	newESPPart  = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	newRootFS   = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
)

func testRewriteIDs(t *testing.T) {
	ids := map[string]string{
		oldRootPart: newRootPart,
		oldESPPart:  newESPPart,
		oldRootFS:   newRootFS,
	}

	// Each document is written with placeholders: R = root PARTUUID,
	// E = ESP PARTUUID, F = root FS UUID.
	docs := []struct {
		name string
		text string
	}{
		{"fstab", "# /etc/fstab: static file system information.\n" +
			"PARTUUID={R} / ext4 rw,discard,errors=remount-ro,x-systemd.growfs 0 1\n" +
			"PARTUUID={E} /boot/efi vfat defaults,umask=077 0 2\n"},
		{"/boot/grub/grub.cfg", "### BEGIN /etc/grub.d/10_linux ###\n" +
			"\tsearch --no-floppy --fs-uuid --set=root {F}\n" +
			"menuentry 'Debian GNU/Linux' --class debian {\n" +
			"\tsearch --no-floppy --fs-uuid --set=root {F}\n" +
			"\tlinux\t/boot/vmlinuz-6.12.0-amd64 root=PARTUUID={R} ro  console=tty0 console=ttyS0,115200\n" +
			"\tinitrd\t/boot/initrd.img-6.12.0-amd64\n" +
			"}\n" +
			"submenu 'Advanced options' {\n" +
			"\tlinux\t/boot/vmlinuz-6.12.0-amd64 root=PARTUUID={R} ro single\n" +
			"}\n"},
		{"ESP EFI/debian/grub.cfg", "search.fs_uuid {F} root hd0,gpt1 \n" +
			"set prefix=($root)'/boot/grub'\r\n" +
			"configfile $prefix/grub.cfg\n"},
	}
	fill := func(tmpl, r, e, f string) string {
		return strings.NewReplacer("{R}", r, "{E}", e, "{F}", f).Replace(tmpl)
	}

	for _, d := range docs {
		in := fill(d.text, oldRootPart, oldESPPart, oldRootFS)
		want := fill(d.text, newRootPart, newESPPart, newRootFS)
		if got := agent.RewriteIDs(in, ids); got != want {
			t.Errorf("%s: RewriteIDs =\n%q\nwant\n%q", d.name, got, want)
		}
	}

	untouched := "UUID=0000-1111 /data xfs defaults 0 0\n\ttrailing space \n"
	if got := agent.RewriteIDs(untouched, ids); got != untouched {
		t.Errorf("RewriteIDs without old identifiers = %q, want the text unchanged", got)
	}
}
