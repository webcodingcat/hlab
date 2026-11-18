package servers_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"regexp"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

const volumeUserData = "#cloud-config\nruncmd:\n  - [sh, -c, \"echo hlab-volume-user-data > /root/marker\"]\n"

// testVendorData holds the rows of vendordata_test.go; TestServers runs
// them.
func testVendorData(t *testing.T) {
	t.Run("VOL-03", func(t *testing.T) {
		vols := []int64{11, 12}
		env, mod, deps := newFakeEnv(t, platformtest.NewFake(), nil)
		mod.SetVolumes(acceptVolumes{})
		createdServer(t, env, "automount", map[string]any{
			"volumes": vols, "automount": true, "user_data": volumeUserData,
		})

		parts := mimeParts(t, seedFile(t, deps.Settings.DataFolder, "user-data"))
		var user, mounts bool
		for _, part := range parts {
			if string(part) == volumeUserData {
				user = true
			}
			all := true
			for _, v := range vols {
				if !bytes.Contains(part, []byte(fmt.Sprintf("/mnt/HC_Volume_%d", v))) {
					all = false
				}
			}
			mounts = mounts || all
		}
		if !user {
			t.Errorf("seed user-data has no part holding the user's user_data unchanged")
		}
		if !mounts {
			t.Errorf("seed user-data has no part mounting volumes %v at /mnt/HC_Volume_<id>", vols)
		}

		vendor := seedFile(t, deps.Settings.DataFolder, "vendor-data")
		for _, want := range []struct{ what, re string }{
			{"udev rule file", `/etc/udev/rules\.d/\S+\.rules`},
			{"scsi-0HC_Volume_ link", `scsi-0HC_Volume_`},
			{"hlab-volume@.service", `hlab-volume@\.service`},
		} {
			if !regexp.MustCompile(want.re).Match(vendor) {
				t.Errorf("vendor-data lacks the %s", want.what)
			}
		}

		plainEnv, plainMod, plainDeps := newFakeEnv(t, platformtest.NewFake(), nil)
		plainMod.SetVolumes(acceptVolumes{})
		createdServer(t, plainEnv, "no-automount", map[string]any{
			"volumes": []int64{13}, "automount": false, "user_data": volumeUserData,
		})
		if got := seedFile(t, plainDeps.Settings.DataFolder, "user-data"); string(got) != volumeUserData {
			t.Errorf("seed user-data without automount volumes:\n%s\nwant the user's user_data unchanged:\n%s", got, volumeUserData)
		}
	})
}

// acceptVolumes are volume hooks that accept every volume and record the
// attach on the server.
type acceptVolumes struct{}

var _ servers.VolumeHooks = acceptVolumes{}

func (acceptVolumes) CheckAttach(store.Tx, int64, string, []int64) error { return nil }

func (acceptVolumes) Attach(_ context.Context, stor *store.Store, serverID int64, ids []int64, _ bool) error {
	return stor.Tx(func(tx store.Tx) error { return servers.SetVolumes(tx, serverID, ids) })
}

func (acceptVolumes) DetachAll(context.Context, *store.Store, int64) error { return nil }

// mimeParts decodes a MIME multipart document (headers, blank line, body)
// into the bodies of its parts.
func mimeParts(t *testing.T, doc []byte) [][]byte {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("user-data is not a MIME document: %v\n%s", err, doc)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mt, "multipart/") {
		t.Fatalf("user-data Content-Type %q (%v), want multipart", msg.Header.Get("Content-Type"), err)
	}
	r := multipart.NewReader(msg.Body, params["boundary"])
	var parts [][]byte
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Fatalf("read user-data part: %v", err)
		}
		body, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("read user-data part: %v", err)
		}
		if strings.EqualFold(p.Header.Get("Content-Transfer-Encoding"), "base64") {
			dec, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(body)), ""))
			if err != nil {
				t.Fatalf("decode base64 user-data part: %v", err)
			}
			body = dec
		}
		parts = append(parts, body)
	}
}
