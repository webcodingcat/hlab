// Package images downloads system images, verifies them against the
// source's checksum file, converts them to a fixed VHD and hands that to a
// platform callback that writes the cached base disk.
package images

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// Source says where an image or ISO comes from.
type Source struct {
	URL         string // image file
	ChecksumURL string // SHA256SUMS or SHA512SUMS listing the image file
	Format      string // "raw", "qcow2" or "iso" (no conversion)
	// FallbackURL and FallbackChecksumURL are tried when URL or ChecksumURL
	// answers 404 (a release moved to an archive directory).
	FallbackURL, FallbackChecksumURL string
	// Path is a local file copied instead of downloaded (own ISOs).
	Path string
	// SHA256 (hex) verifies a Path source, or a URL source without
	// ChecksumURL; empty means the file is not checked.
	SHA256 string
}

// Options configure a Store.
type Options struct {
	// Dir holds the cached bases and the temporary files.
	Dir string
	// Catalog maps image names to their sources.
	Catalog map[string]Source
	// HTTP downloads images and checksum files; nil means http.DefaultClient.
	HTTP *http.Client
	// Convert writes the base dst from the fixed VHD src.
	Convert func(ctx context.Context, src, dst string) error
	// ISOs maps ISO names to their sources; ISOs are cached unchanged.
	ISOs map[string]Source
	// ISOSource is consulted for names ISOs lacks (own ISOs); nil means none.
	ISOSource func(name string) (Source, bool)
	// ISODir holds the cached ISOs (<data>\isos).
	ISODir string
	// RescueImage names the image the rescue base is prepared from.
	RescueImage string
	// PrepareRescue writes the prepared rescue base dst from the cached base
	// of RescueImage.
	PrepareRescue func(ctx context.Context, base, dst string) error
}

// Store caches converted base disks, one per image name.
type Store struct {
	opts  Options
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns a Store.
func New(opts Options) *Store {
	if opts.HTTP == nil {
		opts.HTTP = http.DefaultClient
	}
	return &Store{opts: opts, locks: map[string]*sync.Mutex{}}
}

// Ensure returns the path of the cached base of the image name, downloading,
// verifying and converting it first if it does not exist. Concurrent calls
// for the same name do the work once.
func (s *Store) Ensure(ctx context.Context, name string) (string, error) {
	src, ok := s.opts.Catalog[name]
	if !ok {
		return "", fmt.Errorf("images: unknown image %q", name)
	}
	base := filepath.Join(s.opts.Dir, name+".vhdx")

	lock := s.lock(name)
	lock.Lock()
	defer lock.Unlock()

	if _, err := os.Stat(base); err == nil {
		return base, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("images: %w", err)
	}
	if err := s.build(ctx, name, src, base); err != nil {
		return "", fmt.Errorf("images: %s: %w", name, err)
	}
	return base, nil
}

// EnsureISO returns the path of the cached ISO name (<ISODir>\<name>),
// downloading (or copying a Path source) and verifying it first if it does
// not exist. Concurrent calls
// for the same name download once; on error nothing stays in ISODir.
func (s *Store) EnsureISO(ctx context.Context, name string) (string, error) {
	src, ok := s.opts.ISOs[name]
	own := false
	if !ok && s.opts.ISOSource != nil {
		src, ok = s.opts.ISOSource(name)
		own = ok
	}
	if !ok {
		return "", fmt.Errorf("images: unknown ISO %q", name)
	}
	dst := filepath.Join(s.opts.ISODir, name)

	lock := s.lock("iso/" + name)
	lock.Lock()
	defer lock.Unlock()

	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("images: %w", err)
	}
	if err := s.fetchISO(ctx, src, own, dst); err != nil {
		return "", fmt.Errorf("images: ISO %s: %w", name, err)
	}
	return dst, nil
}

// fetchISO downloads and verifies the ISO of src into dst through a
// temporary file in the same folder; own sources go through fetchOwn.
func (s *Store) fetchISO(ctx context.Context, src Source, own bool, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+"-*.download")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if own {
		err = s.fetchOwn(ctx, src, f)
	} else {
		err = s.fetchSource(ctx, src, f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func (s *Store) lock(name string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[name]
	if !ok {
		l = &sync.Mutex{}
		s.locks[name] = l
	}
	return l
}

// build downloads, verifies and converts the image into base. On error it
// leaves neither base nor temporary files behind.
func (s *Store) build(ctx context.Context, name string, src Source, base string) error {
	if err := os.MkdirAll(s.opts.Dir, 0o750); err != nil {
		return err
	}
	var temps []string
	defer func() {
		for _, p := range temps {
			_ = os.Remove(p)
		}
	}()
	newTemp := func(suffix string) (*os.File, error) {
		f, err := os.CreateTemp(s.opts.Dir, name+"-*"+suffix)
		if err != nil {
			return nil, err
		}
		temps = append(temps, f.Name())
		return f, nil
	}

	dl, err := newTemp(".download")
	if err != nil {
		return err
	}
	if err := s.fetchSource(ctx, src, dl); err != nil {
		_ = dl.Close()
		return err
	}

	raw := dl
	switch src.Format {
	case "raw":
	case "qcow2":
		raw, err = newTemp(".raw")
		if err != nil {
			_ = dl.Close()
			return err
		}
		err = qcow2ToRaw(dl, raw)
		_ = dl.Close()
		if err != nil {
			_ = raw.Close()
			return err
		}
	default:
		_ = dl.Close()
		return fmt.Errorf("unknown image format %q", src.Format)
	}
	err = appendVHDFooter(raw)
	if cerr := raw.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	vhd := raw.Name() + ".vhd"
	if err := os.Rename(raw.Name(), vhd); err != nil {
		return err
	}
	temps = append(temps, vhd)

	partial := filepath.Join(s.opts.Dir, name+".partial.vhdx")
	_ = os.Remove(partial)
	temps = append(temps, partial)
	if err := s.opts.Convert(ctx, vhd, partial); err != nil {
		return fmt.Errorf("convert: %w", err)
	}
	return os.Rename(partial, base)
}
