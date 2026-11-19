package servers_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"reflect"
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

	t.Run("SRV-01/user_data_layers", func(t *testing.T) {
		b64 := base64.StdEncoding.EncodeToString([]byte(volumeUserData))
		gz := gzipped(t, userMIME(t))
		cases := []struct {
			name     string
			userData string
			want     []byte
		}{
			{"plain", volumeUserData, []byte(volumeUserData)},
			{"base64", b64, []byte(volumeUserData)},
			{"base64_with_newline", b64[:20] + "\n" + b64[20:], []byte(b64[:20] + "\n" + b64[20:])},
			{"base64_invalid_characters", "aGxhYi11c2VyLWRhdGE-_w==", []byte("aGxhYi11c2VyLWRhdGE-_w==")},
			{"base64_gzip_mime", base64.StdEncoding.EncodeToString(gz), gz},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				env, _, deps := newFakeEnv(t, platformtest.NewFake(), nil)
				createdServer(t, env, "layers", map[string]any{"user_data": c.userData})
				if got := seedFile(t, deps.Settings.DataFolder, "user-data"); !bytes.Equal(got, c.want) {
					t.Errorf("seed user-data:\n%q\nwant:\n%q", got, c.want)
				}
			})
		}
	})

	t.Run("VOL-03/user_data_layers", func(t *testing.T) {
		vols := []int64{21, 22}
		mimeDoc := userMIME(t)
		nested := nestedMIME(t)
		cases := []struct {
			name     string
			userData string
			// leaves are the parts expected before hlab's part; nil means
			// one base64 text/plain part holding plain.
			leaves []rawPart
			plain  string
		}{
			{"plain", volumeUserData, nil, volumeUserData},
			{"base64", base64.StdEncoding.EncodeToString([]byte(volumeUserData)), nil, volumeUserData},
			{"gzip", base64.StdEncoding.EncodeToString(gzipped(t, []byte(volumeUserData))), nil, volumeUserData},
			{"mime", string(mimeDoc), leafParts(t, mimeDoc), ""},
			{"base64_gzip_mime", base64.StdEncoding.EncodeToString(gzipped(t, mimeDoc)), leafParts(t, mimeDoc), ""},
			{"nested_multipart", string(nested), leafParts(t, nested), ""},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				env, mod, deps := newFakeEnv(t, platformtest.NewFake(), nil)
				mod.SetVolumes(acceptVolumes{})
				createdServer(t, env, "layers", map[string]any{
					"volumes": vols, "automount": true, "user_data": c.userData,
				})
				parts := leafParts(t, seedFile(t, deps.Settings.DataFolder, "user-data"))
				if len(parts) == 0 {
					t.Fatal("seed user-data has no parts")
				}
				hlab := parts[len(parts)-1]
				hlabBody := hlab.body
				if strings.EqualFold(hlab.header.Get("Content-Transfer-Encoding"), "base64") {
					dec, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(hlab.body)), ""))
					if err != nil {
						t.Fatalf("decode hlab's part: %v", err)
					}
					hlabBody = dec
				}
				if mt, _, _ := mime.ParseMediaType(hlab.header.Get("Content-Type")); mt != "text/cloud-config" && !bytes.HasPrefix(hlabBody, []byte("#cloud-config")) {
					t.Errorf("last part (%q) is no cloud-config:\n%s", hlab.header.Get("Content-Type"), hlabBody)
				}
				if !bytes.Contains(hlabBody, []byte("merge_how")) || !bytes.Contains(hlabBody, []byte("runcmd")) {
					t.Errorf("hlab's part lacks merge_how or runcmd:\n%s", hlabBody)
				}
				for _, v := range vols {
					if !bytes.Contains(hlabBody, []byte(fmt.Sprintf("/mnt/HC_Volume_%d", v))) {
						t.Errorf("hlab's part does not mount volume %d at /mnt/HC_Volume_%d", v, v)
					}
				}
				user := parts[:len(parts)-1]

				if c.leaves == nil {
					if len(user) != 1 {
						t.Fatalf("%d parts before hlab's part, want one text/plain part", len(user))
					}
					p := user[0]
					if mt, _, _ := mime.ParseMediaType(p.header.Get("Content-Type")); mt != "text/plain" ||
						!strings.EqualFold(p.header.Get("Content-Transfer-Encoding"), "base64") {
						t.Errorf("user part Content-Type %q, Content-Transfer-Encoding %q, want text/plain, base64",
							p.header.Get("Content-Type"), p.header.Get("Content-Transfer-Encoding"))
					}
					dec, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(p.body)), ""))
					if err != nil || string(dec) != c.plain {
						t.Errorf("user part decodes to %q (%v), want %q", dec, err, c.plain)
					}
					return
				}

				if len(user) != len(c.leaves) {
					t.Fatalf("%d parts before hlab's part, want the user's %d leaf parts", len(user), len(c.leaves))
				}
				for i, want := range c.leaves {
					if !reflect.DeepEqual(user[i].header, want.header) || !bytes.Equal(user[i].body, want.body) {
						t.Errorf("part %d:\n%v\n%q\nwant the user's leaf part unchanged:\n%v\n%q",
							i, user[i].header, user[i].body, want.header, want.body)
					}
				}
			})
		}
	})
}

// rawPart is a MIME leaf part with its headers and its body as written.
type rawPart struct {
	header textproto.MIMEHeader
	body   []byte
}

// leafParts parses a MIME multipart document and returns its leaf parts in
// document order, walking nested multiparts, without decoding any body.
func leafParts(t *testing.T, doc []byte) []rawPart {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("not a MIME document: %v\n%s", err, doc)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mt, "multipart/") {
		t.Fatalf("Content-Type %q (%v), want multipart:\n%s", msg.Header.Get("Content-Type"), err, doc)
	}
	return walkParts(t, msg.Body, params["boundary"])
}

func walkParts(t *testing.T, body io.Reader, boundary string) []rawPart {
	t.Helper()
	r := multipart.NewReader(body, boundary)
	var out []rawPart
	for {
		p, err := r.NextRawPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("read MIME part: %v", err)
		}
		data, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("read MIME part: %v", err)
		}
		mt, params, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if strings.HasPrefix(mt, "multipart/") {
			out = append(out, walkParts(t, bytes.NewReader(data), params["boundary"])...)
			continue
		}
		out = append(out, rawPart{header: p.Header, body: data})
	}
}

// userMIME is a user document as OpenTofu's cloudinit_config writes it
// before gzip: two parts, one with X-Merge-Type.
func userMIME(t *testing.T) []byte {
	t.Helper()
	return mimeDoc(t, "MIMEBOUNDARY", func(w *multipart.Writer) {
		writePart(t, w, textproto.MIMEHeader{
			"Content-Type":              {"text/cloud-config"},
			"Mime-Version":              {"1.0"},
			"Content-Transfer-Encoding": {"7bit"},
			"Content-Disposition":       {`attachment; filename="base.yaml"`},
		}, "#cloud-config\nwrite_files:\n  - path: /root/hlab-user-part-1\n    content: one\n")
		writePart(t, w, textproto.MIMEHeader{
			"Content-Type":              {"text/x-shellscript"},
			"Mime-Version":              {"1.0"},
			"Content-Transfer-Encoding": {"7bit"},
			"Content-Disposition":       {`attachment; filename="user.sh"`},
		}, "#!/bin/sh\necho hlab-user-part-2 > /root/marker\n")
	})
}

// nestedMIME is a user document with a nested multipart between two
// cloud-config parts; the nested one holds a script and a base64 gzip part.
func nestedMIME(t *testing.T) []byte {
	t.Helper()
	innerBoundary := "INNERBOUNDARY"
	var innerBody bytes.Buffer
	inner := multipart.NewWriter(&innerBody)
	if err := inner.SetBoundary(innerBoundary); err != nil {
		t.Fatal(err)
	}
	writePart(t, inner, textproto.MIMEHeader{
		"Content-Type":              {"text/x-shellscript"},
		"Content-Transfer-Encoding": {"7bit"},
		"Content-Disposition":       {`attachment; filename="nested.sh"`},
	}, "#!/bin/sh\necho hlab-nested > /root/nested\n")
	writePart(t, inner, textproto.MIMEHeader{
		"Content-Type":              {"application/gzip"},
		"Content-Transfer-Encoding": {"base64"},
		"Content-Disposition":       {`attachment; filename="packed.gz"`},
	}, base64.StdEncoding.EncodeToString(gzipped(t, []byte("#cloud-config\nruncmd:\n  - [touch, /root/packed]\n"))))
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	return mimeDoc(t, "OUTERBOUNDARY", func(w *multipart.Writer) {
		writePart(t, w, textproto.MIMEHeader{
			"Content-Type": {"text/cloud-config"},
		}, "#cloud-config\nwrite_files:\n  - path: /root/hlab-first\n    content: first\n")
		writePart(t, w, textproto.MIMEHeader{
			"Content-Type": {`multipart/mixed; boundary="` + innerBoundary + `"`},
		}, innerBody.String())
		writePart(t, w, textproto.MIMEHeader{
			"Content-Type": {"text/cloud-config"},
			"X-Merge-Type": {"list(append)+dict(recurse_array)+str()"},
		}, "#cloud-config\nruncmd:\n  - [touch, /root/hlab-last]\n")
	})
}

// mimeDoc writes a multipart/mixed document with MIME-Version and the
// given boundary; parts adds its parts.
func mimeDoc(t *testing.T, boundary string, parts func(w *multipart.Writer)) []byte {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	parts(w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	head := "Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\nMIME-Version: 1.0\r\n\r\n"
	return append([]byte(head), body.Bytes()...)
}

func writePart(t *testing.T, w *multipart.Writer, h textproto.MIMEHeader, body string) {
	t.Helper()
	pw, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(pw, body); err != nil {
		t.Fatal(err)
	}
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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
