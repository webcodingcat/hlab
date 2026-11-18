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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/webcodingcat/hlab/internal/images"
)

// source serves one raw image and its SHA256SUMS file and counts the
// downloads of the image.
type source struct {
	image     []byte
	sums      atomic.Value // string
	downloads atomic.Int64
	srv       *httptest.Server
}

const imageFile = "test-generic-amd64.raw"

func newSource(t *testing.T) *source {
	t.Helper()
	img := bytes.Repeat([]byte{0xA5}, 1<<20) // 1 MiB raw disk
	s := &source{image: img}
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
	s.srv = httptest.NewServer(mux)
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

func TestImages(t *testing.T) {
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
}
