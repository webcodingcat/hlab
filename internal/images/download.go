package images

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// checksum is the expected digest of the image file.
type checksum struct {
	hex  string
	hash func() hash.Hash
}

// errNotFound marks a URL that answered 404.
var errNotFound = errors.New("not found")

// fetchSource downloads the file of src into f and verifies it against its
// checksum file. When the URL or the checksum URL answers 404, the fallback
// pair is tried. A Path source is copied, and a source without ChecksumURL
// is checked against SHA256 when that is set.
func (s *Store) fetchSource(ctx context.Context, src Source, f *os.File) error {
	if src.Path != "" || src.ChecksumURL == "" {
		return s.fetchOwn(ctx, src, f)
	}
	err := s.fetchPair(ctx, src.URL, src.ChecksumURL, f)
	if errors.Is(err, errNotFound) && src.FallbackURL != "" {
		err = s.fetchPair(ctx, src.FallbackURL, src.FallbackChecksumURL, f)
	}
	return err
}

// fetchOwn copies the local file src.Path, or downloads src.URL, into f and
// compares its SHA-256 with src.SHA256 when that is set.
func (s *Store) fetchOwn(ctx context.Context, src Source, f *os.File) error {
	var (
		body io.ReadCloser
		from string
		err  error
	)
	if src.Path != "" {
		from = src.Path
		body, err = os.Open(src.Path)
	} else {
		from = src.URL
		body, err = get(ctx, s.opts.HTTP, src.URL)
	}
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	if src.SHA256 == "" {
		if _, err := io.Copy(f, body); err != nil {
			return fmt.Errorf("copy %s: %w", from, err)
		}
		return nil
	}
	return copyVerified(body, f, from, checksum{hex: strings.ToLower(src.SHA256), hash: sha256.New})
}

// fetchPair downloads fileURL into f and verifies it against the entry for
// its file name in the checksum file at sumURL. Nothing is written to f
// unless fileURL answers 200.
func (s *Store) fetchPair(ctx context.Context, fileURL, sumURL string, f *os.File) error {
	want, err := s.expectedSum(ctx, fileURL, sumURL)
	if err != nil {
		return err
	}
	return download(ctx, s.opts.HTTP, fileURL, f, want)
}

// expectedSum fetches the checksum file and returns the entry for the
// file name of fileURL.
func (s *Store) expectedSum(ctx context.Context, fileURL, sumURL string) (checksum, error) {
	u, err := url.Parse(fileURL)
	if err != nil {
		return checksum{}, fmt.Errorf("image URL: %w", err)
	}
	file := path.Base(u.Path)
	body, err := get(ctx, s.opts.HTTP, sumURL)
	if err != nil {
		return checksum{}, err
	}
	defer func() { _ = body.Close() }()
	sum, err := findSum(body, file)
	if err != nil {
		return checksum{}, fmt.Errorf("checksum file %s: %w", sumURL, err)
	}
	return sum, nil
}

// Fetch downloads the first of urls that answers 200 to dst and verifies its
// SHA-256 (hex). On error nothing stays at dst. A nil client means
// http.DefaultClient.
func Fetch(ctx context.Context, client *http.Client, urls []string, sha256hex, dst string) error {
	if client == nil {
		client = http.DefaultClient
	}
	want := checksum{hex: strings.ToLower(sha256hex), hash: sha256.New}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+"-*.download")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	err = fmt.Errorf("fetch %s: no URL", filepath.Base(dst))
	for _, u := range urls {
		var body io.ReadCloser
		if body, err = get(ctx, client, u); err != nil {
			continue
		}
		err = copyVerified(body, f, u, want)
		_ = body.Close()
		break
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// findSum reads lines "<hex> *<file>" or "<hex>  <file>" and returns the
// one whose file name is exactly file.
func findSum(r io.Reader, file string) (checksum, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		hexSum, rest, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !ok {
			continue
		}
		rest = strings.TrimPrefix(rest, " ")
		rest = strings.TrimPrefix(rest, "*")
		if rest != file {
			continue
		}
		hexSum = strings.ToLower(hexSum)
		if _, err := hex.DecodeString(hexSum); err != nil {
			return checksum{}, fmt.Errorf("invalid digest for %s", file)
		}
		switch len(hexSum) {
		case 2 * sha256.Size:
			return checksum{hex: hexSum, hash: sha256.New}, nil
		case 2 * sha512.Size:
			return checksum{hex: hexSum, hash: sha512.New}, nil
		}
		return checksum{}, fmt.Errorf("digest for %s is neither SHA-256 nor SHA-512", file)
	}
	if err := sc.Err(); err != nil {
		return checksum{}, err
	}
	return checksum{}, fmt.Errorf("no entry for %s", file)
}

// download streams rawURL into f and verifies its digest.
func download(ctx context.Context, client *http.Client, rawURL string, f *os.File, want checksum) error {
	body, err := get(ctx, client, rawURL)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	return copyVerified(body, f, rawURL, want)
}

// copyVerified copies body into w and compares its digest with want.
func copyVerified(body io.Reader, w io.Writer, rawURL string, want checksum) error {
	h := want.hash()
	if _, err := io.Copy(io.MultiWriter(w, h), body); err != nil {
		return fmt.Errorf("download %s: %w", rawURL, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want.hex {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", rawURL, got, want.hex)
	}
	return nil
}

// get answers the body of rawURL; any status but 200 is an error, 404 one
// wrapping errNotFound.
func get(ctx context.Context, client *http.Client, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("GET %s: %s: %w", rawURL, resp.Status, errNotFound)
		}
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	return resp.Body, nil
}
