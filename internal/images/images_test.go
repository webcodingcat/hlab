package images_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/webcodingcat/hlab/internal/images"
)

// source serves one raw image and its SHA256SUMS file and counts the
// downloads of the image. With requireUA it answers 403 to a request whose
// User-Agent does not start with "hlab/"; each path answers 503 to its
// first unavailable requests (all of them when unavailable is negative).
type source struct {
	image       []byte
	sums        atomic.Value // string
	downloads   atomic.Int64
	requireUA   atomic.Bool
	unavailable atomic.Int64
	srv         *httptest.Server

	mu       sync.Mutex
	requests map[string]int64 // per path
}

const imageFile = "test-generic-amd64.raw"

func newSource(t *testing.T) *source {
	t.Helper()
	img := bytes.Repeat([]byte{0xA5}, 1<<20) // 1 MiB raw disk
	s := &source{image: img, requests: map[string]int64{}}
	sum := sha256.Sum256(img)
	s.setSum(hex.EncodeToString(sum[:]))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /img/"+imageFile, func(w http.ResponseWriter, _ *http.Request) {
		s.downloads.Add(1)
		_, _ = w.Write(s.image)
	})
	mux.HandleFunc("GET /img/SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, s.sums.Load().(string))
	})
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.requireUA.Load() && !strings.HasPrefix(r.UserAgent(), "hlab/") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.mu.Lock()
		s.requests[r.URL.Path]++
		n := s.requests[r.URL.Path]
		s.mu.Unlock()
		if u := s.unavailable.Load(); u < 0 || n <= u {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *source) setSum(hexSum string) {
	s.sums.Store(fmt.Sprintf("%s *%s\n", hexSum, imageFile))
}

func (s *source) catalog() map[string]images.Source {
	return map[string]images.Source{
		"debian-13": {
			URL:         s.srv.URL + "/img/" + imageFile,
			ChecksumURL: s.srv.URL + "/img/SHA256SUMS",
			Format:      "raw",
		},
	}
}

// converter is the Convert callback; it counts its calls and writes dst.
type converter struct{ calls atomic.Int64 }

func (c *converter) convert(_ context.Context, src, dst string) error {
	c.calls.Add(1)
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func newStore(t *testing.T, s *source, c *converter) *images.Store {
	t.Helper()
	return images.New(images.Options{
		Dir:     t.TempDir(),
		Catalog: s.catalog(),
		HTTP:    s.srv.Client(),
		Convert: c.convert,
	})
}

// isoSource serves one ISO and its SHA256SUMS file under /current/ and,
// when archived, only under /archive/ (/current/ answers 404), and counts
// the downloads of the ISO.
type isoSource struct {
	iso       []byte
	sums      atomic.Value // string
	archived  atomic.Bool
	downloads atomic.Int64
	srv       *httptest.Server
}

const isoFile = "test-live-amd64.iso"

func newISOSource(t *testing.T) *isoSource {
	t.Helper()
	s := &isoSource{iso: bytes.Repeat([]byte{0x5A}, 1<<19)}
	sum := sha256.Sum256(s.iso)
	s.setSum(hex.EncodeToString(sum[:]))
	mux := http.NewServeMux()
	for _, dir := range []string{"current", "archive"} {
		gone := func() bool { return (dir == "current") == s.archived.Load() }
		mux.HandleFunc("GET /"+dir+"/"+isoFile, func(w http.ResponseWriter, r *http.Request) {
			if gone() {
				http.NotFound(w, r)
				return
			}
			s.downloads.Add(1)
			_, _ = w.Write(s.iso)
		})
		mux.HandleFunc("GET /"+dir+"/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
			if gone() {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, s.sums.Load().(string))
		})
	}
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *isoSource) setSum(hexSum string) {
	s.sums.Store(fmt.Sprintf("%s *%s\n", hexSum, isoFile))
}

func newISOStore(t *testing.T, s *isoSource) (*images.Store, string) {
	t.Helper()
	isoDir := filepath.Join(t.TempDir(), "isos")
	u := s.srv.URL
	return images.New(images.Options{
		Dir: t.TempDir(),
		ISOs: map[string]images.Source{
			"test-live": {
				URL:                 u + "/current/" + isoFile,
				ChecksumURL:         u + "/current/SHA256SUMS",
				Format:              "iso",
				FallbackURL:         u + "/archive/" + isoFile,
				FallbackChecksumURL: u + "/archive/SHA256SUMS",
			},
		},
		ISODir: isoDir,
		HTTP:   s.srv.Client(),
	}), isoDir
}

// isoDirFiles lists the files in dir; a missing dir has none.
func isoDirFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read ISO folder: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestImages(t *testing.T) {
	t.Run("CAT-08", func(t *testing.T) {
		ctx := context.Background()

		// First use downloads and verifies, caches under the ISO folder;
		// the second use downloads nothing.
		src := newISOSource(t)
		st, isoDir := newISOStore(t, src)
		path, err := st.EnsureISO(ctx, "test-live")
		if err != nil {
			t.Fatalf("first EnsureISO: %v", err)
		}
		if filepath.Dir(path) != isoDir {
			t.Errorf("EnsureISO = %q, want a file in %q", path, isoDir)
		}
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, src.iso) {
			t.Errorf("cached ISO %q: %d bytes (%v), want the served ISO unchanged (%d bytes)", path, len(data), err, len(src.iso))
		}
		if got := src.downloads.Load(); got != 1 {
			t.Errorf("downloads after first EnsureISO = %d, want 1", got)
		}
		again, err := st.EnsureISO(ctx, "test-live")
		if err != nil {
			t.Fatalf("second EnsureISO: %v", err)
		}
		if again != path {
			t.Errorf("second EnsureISO = %q, want cached %q", again, path)
		}
		if got := src.downloads.Load(); got != 1 {
			t.Errorf("downloads after second EnsureISO = %d, want 1", got)
		}

		// A checksum mismatch errors and caches nothing.
		bad := newISOSource(t)
		bad.setSum(hex.EncodeToString(make([]byte, sha256.Size)))
		badStore, badDir := newISOStore(t, bad)
		if p, err := badStore.EnsureISO(ctx, "test-live"); err == nil {
			t.Errorf("EnsureISO with checksum mismatch = %q, want an error", p)
		}
		if files := isoDirFiles(t, badDir); len(files) != 0 {
			t.Errorf("ISO folder after checksum mismatch holds %v, want nothing", files)
		}

		// Primary URLs answering 404 fall back to the archive.
		moved := newISOSource(t)
		moved.archived.Store(true)
		movedStore, _ := newISOStore(t, moved)
		p, err := movedStore.EnsureISO(ctx, "test-live")
		if err != nil {
			t.Fatalf("EnsureISO with primary 404: %v", err)
		}
		if data, err := os.ReadFile(p); err != nil || !bytes.Equal(data, moved.iso) {
			t.Errorf("ISO from the fallback %q: %d bytes (%v), want the served ISO", p, len(data), err)
		}
		if got := moved.downloads.Load(); got != 1 {
			t.Errorf("downloads from the fallback = %d, want 1", got)
		}

		// An unknown name errors.
		if p, err := st.EnsureISO(ctx, "no-such-iso"); err == nil {
			t.Errorf("EnsureISO of an unknown name = %q, want an error", p)
		}
	})
	t.Run("CAT-10", func(t *testing.T) {
		ctx := context.Background()
		const ownName = "own-100000-0123abcd.iso"
		wrongSum := hex.EncodeToString(make([]byte, sha256.Size))

		cases := []struct {
			name string
			// source returns the own source; src serves the URL and local
			// is a local copy of the same ISO content.
			source func(src *isoSource, local string) images.Source
			ok     bool
		}{
			{"url with SHA-256", func(s *isoSource, _ string) images.Source {
				sum := sha256.Sum256(s.iso)
				return images.Source{URL: s.srv.URL + "/current/" + isoFile, SHA256: hex.EncodeToString(sum[:]), Format: "iso"}
			}, true},
			{"url with a wrong SHA-256", func(s *isoSource, _ string) images.Source {
				return images.Source{URL: s.srv.URL + "/current/" + isoFile, SHA256: wrongSum, Format: "iso"}
			}, false},
			{"url without SHA-256", func(s *isoSource, _ string) images.Source {
				return images.Source{URL: s.srv.URL + "/current/" + isoFile, Format: "iso"}
			}, true},
			{"local file with SHA-256", func(s *isoSource, local string) images.Source {
				sum := sha256.Sum256(s.iso)
				return images.Source{Path: local, SHA256: hex.EncodeToString(sum[:]), Format: "iso"}
			}, true},
			{"local file with a wrong SHA-256", func(_ *isoSource, local string) images.Source {
				return images.Source{Path: local, SHA256: wrongSum, Format: "iso"}
			}, false},
			{"missing local file", func(_ *isoSource, local string) images.Source {
				return images.Source{Path: filepath.Join(filepath.Dir(local), "missing.iso"), Format: "iso"}
			}, false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				src := newISOSource(t)
				// The checksum file lists a wrong sum: an own URL source is
				// checked against its SHA-256 only.
				src.setSum(wrongSum)
				local := filepath.Join(t.TempDir(), "local.iso")
				if err := os.WriteFile(local, src.iso, 0o644); err != nil {
					t.Fatal(err)
				}
				own := c.source(src, local)
				isoDir := filepath.Join(t.TempDir(), "isos")
				st := images.New(images.Options{
					Dir:    t.TempDir(),
					ISODir: isoDir,
					HTTP:   src.srv.Client(),
					ISOSource: func(name string) (images.Source, bool) {
						if name == ownName {
							return own, true
						}
						return images.Source{}, false
					},
				})

				path, err := st.EnsureISO(ctx, ownName)
				if !c.ok {
					if err == nil {
						t.Errorf("EnsureISO = %q, want an error", path)
					}
					if files := isoDirFiles(t, isoDir); len(files) != 0 {
						t.Errorf("ISO folder after the failed EnsureISO holds %v, want nothing", files)
					}
					return
				}
				if err != nil {
					t.Fatalf("EnsureISO: %v", err)
				}
				if filepath.Dir(path) != isoDir {
					t.Errorf("EnsureISO = %q, want a file in %q", path, isoDir)
				}
				if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, src.iso) {
					t.Errorf("cached ISO %q: %d bytes (%v), want the source unchanged (%d bytes)", path, len(data), err, len(src.iso))
				}
				again, err := st.EnsureISO(ctx, ownName)
				if err != nil || again != path {
					t.Errorf("second EnsureISO = %q, %v; want cached %q", again, err, path)
				}
				wantDownloads := int64(0)
				if own.URL != "" {
					wantDownloads = 1
				}
				if got := src.downloads.Load(); got != wantDownloads {
					t.Errorf("downloads after two EnsureISO = %d, want %d", got, wantDownloads)
				}
				if own.Path != "" {
					if data, err := os.ReadFile(own.Path); err != nil || !bytes.Equal(data, src.iso) {
						t.Errorf("local source after the copy: %d bytes (%v), want it unchanged", len(data), err)
					}
				}
			})
		}
	})
	t.Run("CAT-07", func(t *testing.T) {
		ctx := context.Background()

		// First use downloads, verifies, converts once; second use reuses.
		src := newSource(t)
		conv := &converter{}
		st := newStore(t, src, conv)
		base, err := st.Ensure(ctx, "debian-13")
		if err != nil {
			t.Fatalf("first Ensure: %v", err)
		}
		if _, err := os.Stat(base); err != nil {
			t.Fatalf("cached base %q: %v", base, err)
		}
		if got := src.downloads.Load(); got != 1 {
			t.Errorf("downloads after first Ensure = %d, want 1", got)
		}
		if got := conv.calls.Load(); got != 1 {
			t.Errorf("conversions after first Ensure = %d, want 1", got)
		}
		again, err := st.Ensure(ctx, "debian-13")
		if err != nil {
			t.Fatalf("second Ensure: %v", err)
		}
		if again != base {
			t.Errorf("second Ensure = %q, want cached %q", again, base)
		}
		if d, c := src.downloads.Load(), conv.calls.Load(); d != 1 || c != 1 {
			t.Errorf("after second Ensure: downloads %d, conversions %d, want 1 and 1", d, c)
		}

		// A checksum mismatch fails and leaves no cached base.
		bad := newSource(t)
		bad.setSum(hex.EncodeToString(make([]byte, sha256.Size)))
		badConv := &converter{}
		badStore := newStore(t, bad, badConv)
		if p, err := badStore.Ensure(ctx, "debian-13"); err == nil {
			t.Fatalf("Ensure with checksum mismatch = %q, want an error", p)
		}
		if got := badConv.calls.Load(); got != 0 {
			t.Errorf("conversions after checksum mismatch = %d, want 0", got)
		}
		good := sha256.Sum256(bad.image)
		bad.setSum(hex.EncodeToString(good[:]))
		if _, err := badStore.Ensure(ctx, "debian-13"); err != nil {
			t.Fatalf("Ensure after fixing the checksum: %v", err)
		}
		if got := bad.downloads.Load(); got != 2 {
			t.Errorf("downloads after mismatch and retry = %d, want 2 (nothing cached by the failed attempt)", got)
		}

		// Concurrent first uses convert once.
		par := newSource(t)
		parConv := &converter{}
		parStore := newStore(t, par, parConv)
		const n = 8
		start := make(chan struct{})
		paths := make([]string, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				paths[i], errs[i] = parStore.Ensure(ctx, "debian-13")
			}()
		}
		close(start)
		wg.Wait()
		for i := range n {
			if errs[i] != nil {
				t.Fatalf("concurrent Ensure %d: %v", i, errs[i])
			}
			if paths[i] != paths[0] {
				t.Errorf("concurrent Ensure %d = %q, want %q", i, paths[i], paths[0])
			}
		}
		if got := parConv.calls.Load(); got != 1 {
			t.Errorf("conversions under concurrent use = %d, want 1", got)
		}
	})

	t.Run("CAT-09_checksums", func(t *testing.T) {
		ctx := context.Background()
		const (
			other    = "other-generic-amd64.qcow2"
			otherSum = "1111111111111111111111111111111111111111111111111111111111111111"
		)
		cases := []struct {
			name string
			sums func(hexSum string) string
		}{
			{"gnu", func(h string) string {
				return fmt.Sprintf("%s  %s\n", h, imageFile)
			}},
			{"bsd", func(h string) string {
				return fmt.Sprintf("# %s: 1048576 bytes\nSHA256 (%s) = %s\n", imageFile, imageFile, h)
			}},
			{"pgp_clearsigned_bsd", func(h string) string {
				return "-----BEGIN PGP SIGNED MESSAGE-----\n" +
					"Hash: SHA256\n" +
					"\n" +
					fmt.Sprintf("# %s: 1048576 bytes\n", other) +
					fmt.Sprintf("SHA256 (%s) = %s\n", other, otherSum) +
					fmt.Sprintf("# %s: 1048576 bytes\n", imageFile) +
					fmt.Sprintf("SHA256 (%s) = %s\n", imageFile, h) +
					"-----BEGIN PGP SIGNATURE-----\n" +
					"\n" +
					"iQIzBAEBCAAdFiEEexampleexampleexampleexampleAAoJEexample\n" +
					"=AbCd\n" +
					"-----END PGP SIGNATURE-----\n"
			}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				src := newSource(t)
				sum := sha256.Sum256(src.image)
				src.sums.Store(c.sums(hex.EncodeToString(sum[:])))
				conv := &converter{}
				st := newStore(t, src, conv)
				base, err := st.Ensure(ctx, "debian-13")
				if err != nil {
					t.Fatalf("Ensure with a %s checksum file: %v", c.name, err)
				}
				if _, err := os.Stat(base); err != nil {
					t.Errorf("cached base %q: %v", base, err)
				}
				if d, n := src.downloads.Load(), conv.calls.Load(); d != 1 || n != 1 {
					t.Errorf("downloads %d, conversions %d, want 1 and 1", d, n)
				}
			})
		}

		t.Run("bsd_wrong_digest", func(t *testing.T) {
			src := newSource(t)
			wrong := hex.EncodeToString(make([]byte, sha256.Size))
			src.sums.Store(fmt.Sprintf("SHA256 (%s) = %s\n", imageFile, wrong))
			conv := &converter{}
			st := newStore(t, src, conv)
			if p, err := st.Ensure(ctx, "debian-13"); err == nil {
				t.Fatalf("Ensure with a wrong BSD digest = %q, want an error", p)
			}
			if got := conv.calls.Load(); got != 0 {
				t.Errorf("conversions after the wrong digest = %d, want 0", got)
			}
			good := sha256.Sum256(src.image)
			src.sums.Store(fmt.Sprintf("SHA256 (%s) = %s\n", imageFile, hex.EncodeToString(good[:])))
			before := src.downloads.Load()
			if _, err := st.Ensure(ctx, "debian-13"); err != nil {
				t.Fatalf("Ensure after fixing the digest: %v", err)
			}
			if got := src.downloads.Load() - before; got != 1 {
				t.Errorf("downloads after fixing the digest = %d, want 1 (nothing cached by the failed attempt)", got)
			}
		})
	})

	t.Run("CAT-09_mirror", func(t *testing.T) {
		ctx := context.Background()
		cases := []struct {
			name        string
			requireUA   bool
			unavailable int64 // 503 answers per path; negative: always
			ok          bool
		}{
			{"user_agent", true, 0, true},
			{"503_once", false, 1, true},
			{"503_always", false, -1, false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				src := newSource(t)
				src.requireUA.Store(c.requireUA)
				src.unavailable.Store(c.unavailable)
				conv := &converter{}
				st := newStore(t, src, conv)
				base, err := st.Ensure(ctx, "debian-13")
				if !c.ok {
					if err == nil {
						t.Fatalf("Ensure from a source answering 503 every time = %q, want an error", base)
					}
					if got := conv.calls.Load(); got != 0 {
						t.Errorf("conversions after the failed Ensure = %d, want 0", got)
					}
					// Nothing is cached: once the source answers, the
					// image is downloaded and converted.
					src.unavailable.Store(0)
					if _, err := st.Ensure(ctx, "debian-13"); err != nil {
						t.Fatalf("Ensure after the source recovered: %v", err)
					}
					if d, n := src.downloads.Load(), conv.calls.Load(); d != 1 || n != 1 {
						t.Errorf("after the source recovered: downloads %d, conversions %d, want 1 and 1", d, n)
					}
					return
				}
				if err != nil {
					t.Fatalf("Ensure: %v", err)
				}
				if _, err := os.Stat(base); err != nil {
					t.Errorf("cached base %q: %v", base, err)
				}
				if d, n := src.downloads.Load(), conv.calls.Load(); d != 1 || n != 1 {
					t.Errorf("downloads %d, conversions %d, want 1 and 1", d, n)
				}
			})
		}
	})
}
