package imagefmt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testDisk returns the disk that testdata/*.qcow2 hold (written by qemu-img
// convert, with 4 KiB clusters, once plain and once compressed): every
// third 4 KiB block filled with its number and starting with "block<n>",
// the rest zeros, and a last cluster cut short.
func testDisk() []byte {
	const size = 1024*1024 + 1536
	disk := make([]byte, size)
	for block := 0; block*4096 < size; block += 3 {
		start := block * 4096
		end := min(start+4096, size)
		for i := start; i < end; i++ {
			disk[i] = byte(block%251 + 1)
		}
		tag := fmt.Sprintf("block%03d", block)
		copy(disk[start:end], tag)
	}
	return disk
}

// buffer is a growing io.WriterAt.
type buffer struct{ data []byte }

func (b *buffer) WriteAt(p []byte, offset int64) (int, error) {
	if end := int(offset) + len(p); end > len(b.data) {
		b.data = append(b.data, make([]byte, end-len(b.data))...)
	}
	return copy(b.data[offset:], p), nil
}

func TestConvertQCOW2ReadsPlainAndCompressedClusters(t *testing.T) {
	want := testDisk()
	for _, name := range []string{"plain.qcow2", "compressed.qcow2"} {
		t.Run(name, func(t *testing.T) {
			src, err := os.Open(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = src.Close() }()
			size, err := QCOW2Size(src)
			if err != nil || size != uint64(len(want)) {
				t.Fatalf("QCOW2Size = %d, %v; want %d", size, err, len(want))
			}
			dst := &buffer{data: make([]byte, size)}
			if err := ConvertQCOW2(src, dst); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dst.data, want) {
				t.Error("the converted disk differs from the original")
			}
		})
	}
}

func TestConvertQCOW2RefusesWhatItCannotRead(t *testing.T) {
	plain, err := os.ReadFile(filepath.Join("testdata", "plain.qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	withBacking := bytes.Clone(plain)
	binary.BigEndian.PutUint64(withBacking[8:], 512)
	withExternalData := bytes.Clone(plain)
	binary.BigEndian.PutUint64(withExternalData[72:], 1<<2)
	for name, file := range map[string][]byte{"backing file": withBacking, "external data": withExternalData} {
		err := ConvertQCOW2(bytes.NewReader(file), &buffer{})
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
	if err := ConvertQCOW2(bytes.NewReader([]byte("not a disk at all, but long enough for a header....................................")), &buffer{}); err == nil {
		t.Error("a file that is not qcow2 converted")
	}
}

func TestVHDFooterIsValid(t *testing.T) {
	created := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	footer, err := VHDFooter(40<<30, created, [16]byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	be := binary.BigEndian
	if string(footer[:8]) != "conectix" || be.Uint32(footer[60:]) != 2 || be.Uint64(footer[48:]) != 40<<30 {
		t.Errorf("cookie, type or size wrong: % x", footer[:64])
	}
	stored := be.Uint32(footer[64:])
	copy(footer[64:68], []byte{0, 0, 0, 0})
	var sum uint32
	for _, b := range footer {
		sum += uint32(b)
	}
	if stored != ^sum {
		t.Errorf("checksum %#x, want %#x", stored, ^sum)
	}
	// the specification's geometry of 40 GiB: 255 sectors per track and 16
	// heads above 65535 × 16 × 63 sectors, so 20560 cylinders
	if geometry := be.Uint32(footer[56:]); geometry != 0x505010ff {
		t.Errorf("geometry %#x, want 0x505010ff", geometry)
	}
	if _, err := VHDFooter(1000, created, [16]byte{}); err == nil {
		t.Error("a size that is no multiple of 512 was taken")
	}
}
