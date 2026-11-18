package images

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

// checksum is the expected digest of the image file.
type checksum struct {
	hex  string
	hash func() hash.Hash
}

// expectedSum fetches the checksum file and returns the entry for the
// image's file name.
func (s *Store) expectedSum(ctx context.Context, src Source) (checksum, error) {
	u, err := url.Parse(src.URL)
	if err != nil {
		return checksum{}, fmt.Errorf("image URL: %w", err)
	}
	file := path.Base(u.Path)
	body, err := s.get(ctx, src.ChecksumURL)
	if err != nil {
		return checksum{}, err
	}
	defer func() { _ = body.Close() }()
	sum, err := findSum(body, file)
	if err != nil {
		return checksum{}, fmt.Errorf("checksum file %s: %w", src.ChecksumURL, err)
	}
	return sum, nil
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
func (s *Store) download(ctx context.Context, rawURL string, f *os.File, want checksum) error {
	body, err := s.get(ctx, rawURL)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	h := want.hash()
	if _, err := io.Copy(io.MultiWriter(f, h), body); err != nil {
		return fmt.Errorf("download %s: %w", rawURL, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want.hex {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", rawURL, got, want.hex)
	}
	return nil
}

func (s *Store) get(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.opts.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	return resp.Body, nil
}
