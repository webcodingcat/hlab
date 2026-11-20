package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestIcons(t *testing.T) {
	t.Run("UI-01", func(t *testing.T) {
		root := t.TempDir()
		for _, d := range []string{"cmd/hlab-tray/icons", "cmd/hlab", "cmd/hlabd", "ui/frontend/src/assets", "docs/images"} {
			if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := Generate(root); err != nil {
			t.Fatalf("Generate: %v", err)
		}

		app := readICO(t, filepath.Join(root, "cmd", "hlab-tray", "icons", "app.ico"))
		if got, want := sizes(app), []int{16, 20, 24, 32, 48, 256}; !slices.Equal(got, want) {
			t.Errorf("app.ico sizes %v, want %v", got, want)
		}

		for _, state := range []string{"running", "busy", "stopped", "error"} {
			icon := readICO(t, filepath.Join(root, "cmd", "hlab-tray", "icons", state+".ico"))
			if got, want := sizes(icon), []int{16, 20, 24, 32}; !slices.Equal(got, want) {
				t.Errorf("%s.ico sizes %v, want %v", state, got, want)
			}
			for size, img := range icon {
				base, ok := app[size]
				if !ok {
					continue
				}
				differs, outside := 0, image.Point{-1, -1}
				for y := range size {
					for x := range size {
						if img.NRGBAAt(x, y) == base.NRGBAAt(x, y) {
							continue
						}
						differs++
						if (x < size/2 || y < size/2) && outside.X < 0 {
							outside = image.Pt(x, y)
						}
					}
				}
				if differs == 0 {
					t.Errorf("%s.ico at %d px equals the app icon: no status dot", state, size)
				}
				if outside.X >= 0 {
					t.Errorf("%s.ico at %d px differs from the app icon at %v, outside the lower-right corner", state, size, outside)
				}
			}
		}

		for _, prog := range []string{"hlab-tray", "hlab", "hlabd"} {
			path := filepath.Join(root, "cmd", prog, "rsrc_windows_amd64.syso")
			ids := groupIconIDs(t, path)
			for _, want := range []uint32{3, 32512} {
				if !slices.Contains(ids, want) {
					t.Errorf("%s: group icon IDs %v, want %d among them", filepath.ToSlash(path[len(root)+1:]), ids, want)
				}
			}
		}
	})
}

// sizes lists the frame sizes of an icon, ascending.
func sizes(icon map[int]*image.NRGBA) []int {
	var out []int
	for s := range icon {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// readICO decodes every frame of an ICO file (PNG or 32-bit BMP) by its
// size; frames must be square.
func readICO(t *testing.T, path string) map[int]*image.NRGBA {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:]) != 0 || binary.LittleEndian.Uint16(data[2:]) != 1 {
		t.Fatalf("%s is not an ICO file", path)
	}
	n := int(binary.LittleEndian.Uint16(data[4:]))
	out := map[int]*image.NRGBA{}
	for i := range n {
		e := 6 + 16*i
		if len(data) < e+16 {
			t.Fatalf("%s: directory entry %d cut off", path, i)
		}
		w, h := int(data[e]), int(data[e+1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		size := int(binary.LittleEndian.Uint32(data[e+8:]))
		off := int(binary.LittleEndian.Uint32(data[e+12:]))
		if off+size > len(data) || w != h {
			t.Fatalf("%s: frame %d (%dx%d) is invalid", path, i, w, h)
		}
		img, err := decodeFrame(data[off : off+size])
		if err != nil {
			t.Fatalf("%s: frame %d: %v", path, i, err)
		}
		if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
			t.Fatalf("%s: frame %d is %dx%d, its entry says %dx%d", path, i, b.Dx(), b.Dy(), w, h)
		}
		if _, dup := out[w]; dup {
			t.Fatalf("%s: two frames of %d px", path, w)
		}
		out[w] = img
	}
	return out
}

func decodeFrame(b []byte) (*image.NRGBA, error) {
	if bytes.HasPrefix(b, []byte("\x89PNG")) {
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		out := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
		for y := range out.Rect.Dy() {
			for x := range out.Rect.Dx() {
				c := img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y)
				out.SetNRGBA(x, y, color.NRGBAModel.Convert(c).(color.NRGBA))
			}
		}
		return out, nil
	}
	if len(b) < 40 || binary.LittleEndian.Uint32(b) < 40 {
		return nil, errors.New("neither PNG nor BMP")
	}
	hdr := int(binary.LittleEndian.Uint32(b))
	w := int(int32(binary.LittleEndian.Uint32(b[4:])))
	h := int(int32(binary.LittleEndian.Uint32(b[8:]))) / 2 // XOR and AND mask
	bpp := binary.LittleEndian.Uint16(b[14:])
	if bpp != 32 || binary.LittleEndian.Uint32(b[16:]) != 0 {
		return nil, fmt.Errorf("BMP frame with %d bits per pixel or compression, want 32-bit uncompressed", bpp)
	}
	px := b[hdr:]
	if len(px) < w*h*4 {
		return nil, errors.New("BMP frame pixel data cut off")
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		row := px[(h-1-y)*w*4:] // bottom-up
		for x := range w {
			p := row[x*4:]
			out.SetNRGBA(x, y, color.NRGBA{R: p[2], G: p[1], B: p[0], A: p[3]})
		}
	}
	return out, nil
}

// rtGroupIcon is the resource type of an icon group.
const rtGroupIcon = 14

// groupIconIDs reads the object file's resource section and returns the
// IDs of its icon groups.
func groupIconIDs(t *testing.T, path string) []uint32 {
	t.Helper()
	f, err := pe.Open(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	defer f.Close()
	var rsrc []byte
	for _, s := range f.Sections {
		if strings.HasPrefix(s.Name, ".rsrc") {
			if rsrc, err = s.Data(); err != nil {
				t.Fatalf("%s: read %s: %v", path, s.Name, err)
			}
			break
		}
	}
	if rsrc == nil {
		t.Fatalf("%s has no .rsrc section", path)
	}
	// entries lists the (id, offset, subdirectory) entries of the resource
	// directory at off.
	type entry struct {
		id, off uint32
		dir     bool
	}
	entries := func(off uint32) []entry {
		if int(off)+16 > len(rsrc) {
			t.Fatalf("%s: resource directory at %d out of range", path, off)
		}
		named := uint32(binary.LittleEndian.Uint16(rsrc[off+12:]))
		ids := uint32(binary.LittleEndian.Uint16(rsrc[off+14:]))
		var out []entry
		for i := range named + ids {
			e := off + 16 + 8*i
			if int(e)+8 > len(rsrc) {
				t.Fatalf("%s: resource entry at %d out of range", path, e)
			}
			name := binary.LittleEndian.Uint32(rsrc[e:])
			data := binary.LittleEndian.Uint32(rsrc[e+4:])
			if name&0x80000000 != 0 {
				continue // a named entry
			}
			out = append(out, entry{id: name, off: data &^ 0x80000000, dir: data&0x80000000 != 0})
		}
		return out
	}
	var ids []uint32
	for _, typ := range entries(0) {
		if typ.id != rtGroupIcon || !typ.dir {
			continue
		}
		for _, e := range entries(typ.off) {
			ids = append(ids, e.id)
		}
	}
	return ids
}
