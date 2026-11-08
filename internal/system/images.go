package system

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/imagefmt"
	"github.com/webcodingcat/hlab/internal/platform"
)

// ImageSource is an official cloud image: its qcow2 file and the list of
// checksums that names it.
type ImageSource struct {
	URL       string
	Checksums string
}

// LoadedImage is a system image in the cache.
type LoadedImage struct {
	File   string // the dynamic VHDX, a server disk's parent
	SHA256 string // of the file loaded, hex
}

// LoadImage loads a system image into the cache folder: it downloads the
// qcow2 file, checks it against the source's list of checksums, writes the
// disk as a fixed VHD and lets the hypervisor make it a dynamic VHDX named
// by the file's SHA-256 (images/cache/<sha256>.vhdx). The files in between
// go. It takes a minute or so: the images are some hundred MB.
func (s *System) LoadImage(ctx context.Context, source ImageSource, cacheDir string) (LoadedImage, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return LoadedImage{}, err
	}
	work, err := os.MkdirTemp(cacheDir, "load-")
	if err != nil {
		return LoadedImage{}, err
	}
	defer func() { _ = os.RemoveAll(work) }() // a folder left behind does no harm

	want, err := sourceChecksum(ctx, source)
	if err != nil {
		return LoadedImage{}, err
	}
	qcow2 := filepath.Join(work, "image.qcow2")
	sum, err := download(ctx, source.URL, qcow2, want)
	if err != nil {
		return LoadedImage{}, err
	}
	vhd := filepath.Join(work, "image.vhd")
	if err := writeVHD(qcow2, vhd); err != nil {
		return LoadedImage{}, fmt.Errorf("converting %s: %w", source.URL, err)
	}
	loaded := LoadedImage{File: filepath.Join(cacheDir, sum+".vhdx"), SHA256: sum}
	if _, err := os.Stat(loaded.File); err == nil {
		return loaded, nil // the same file, loaded before
	}
	if err := s.platform.Hypervisor.FlattenDisk(ctx, vhd, loaded.File); err != nil {
		return LoadedImage{}, fmt.Errorf("converting %s to VHDX: %w", source.URL, err)
	}
	return loaded, nil
}

// CleanDir removes from a folder of disks what keep does not name: from
// the image cache the images that no cache entry and no server's disk needs
// any more (an image whose source changed in the catalog was loaded anew),
// from the appliance's folder the releases and system disks of earlier
// appliances; and what work HLab's end cut short left. It returns what it
// removed. A disk that a VM still uses stays (the hypervisor refuses to
// delete it).
func (s *System) CleanDir(ctx context.Context, dir string, keep []string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, entry := range entries {
		file := filepath.Join(dir, entry.Name())
		if slices.Contains(keep, file) {
			continue
		}
		if entry.IsDir() {
			err = os.RemoveAll(file)
		} else if err = s.platform.Hypervisor.DeleteDisk(ctx, file); errors.Is(err, platform.ErrNotFound) {
			err = os.Remove(file) // a file the hypervisor does not know as a disk
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, file)
	}
	return removed, errors.Join(errs...)
}

// checksum is a file's expected checksum and the hash that computes it.
type checksum struct {
	hex     string
	newHash func() hash.Hash
}

// sourceChecksum reads the list of checksums and returns the one of the
// source's file: SHA-256 or SHA-512, told apart by its length.
func sourceChecksum(ctx context.Context, source ImageSource) (checksum, error) {
	body, err := get(ctx, source.Checksums)
	if err != nil {
		return checksum{}, err
	}
	defer func() { _ = body.Close() }()
	name := path.Base(source.URL)
	lines := bufio.NewScanner(body)
	for lines.Scan() {
		// "<hex>  <name>", or "<hex> *<name>" for a binary file
		fields := strings.Fields(lines.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		switch len(fields[0]) {
		case 2 * sha256.Size:
			return checksum{hex: fields[0], newHash: sha256.New}, nil
		case 2 * sha512.Size:
			return checksum{hex: fields[0], newHash: sha512.New}, nil
		}
	}
	if err := lines.Err(); err != nil {
		return checksum{}, fmt.Errorf("reading %s: %w", source.Checksums, err)
	}
	return checksum{}, fmt.Errorf("%s names no checksum of %s", source.Checksums, name)
}

// download writes a URL's body to dst, checks it against want and returns
// its SHA-256.
func download(ctx context.Context, url, dst string, want checksum) (string, error) {
	body, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	file, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }() // a second close after the one below
	ownSum, wantSum := sha256.New(), want.newHash()
	if _, err := io.Copy(io.MultiWriter(file, ownSum, wantSum), body); err != nil {
		return "", fmt.Errorf("downloading %s: %w", url, err)
	}
	if got := hex.EncodeToString(wantSum.Sum(nil)); got != strings.ToLower(want.hex) {
		return "", fmt.Errorf("%s: the checksum is %s, the list says %s", url, got, want.hex)
	}
	return hex.EncodeToString(ownSum.Sum(nil)), file.Close()
}

// get starts a GET request and returns the body of a 200 answer.
func get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

// writeVHD writes the disk of a qcow2 file as a fixed VHD: the raw disk,
// then the footer. The file is truncated to its size first, so that the
// clusters the conversion skips read as zeros.
func writeVHD(qcow2, vhd string) error {
	src, err := os.Open(qcow2)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	size, err := imagefmt.QCOW2Size(src)
	if err != nil {
		return err
	}
	var id [16]byte
	_, _ = rand.Read(id[:])
	footer, err := imagefmt.VHDFooter(size, time.Now(), id)
	if err != nil {
		return err
	}
	dst, err := os.Create(vhd)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }() // a second close after the one below
	if err := dst.Truncate(int64(size)); err != nil {
		return err
	}
	if err := imagefmt.ConvertQCOW2(src, dst); err != nil {
		return err
	}
	if _, err := dst.WriteAt(footer, int64(size)); err != nil {
		return err
	}
	return errors.Join(dst.Sync(), dst.Close())
}
