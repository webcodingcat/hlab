package vhdx

import (
	"compress/gzip"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// sample copies testdata/hyperv-1g.vhdx.gz, an empty dynamic VHDX of 1 GB
// that Hyper-V's New-VHD wrote, into a file of the test's own.
func sample(t *testing.T) string {
	t.Helper()
	in, err := os.Open(filepath.Join("testdata", "hyperv-1g.vhdx.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	unzipped, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(unzipped)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "disk.vhdx")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiskIDReadsWhatHyperVWrote(t *testing.T) {
	id, err := DiskID(sample(t))
	if err != nil {
		t.Fatal(err)
	}
	// Get-VHD showed the sample's DiskIdentifier as this GUID
	if want := guid("388A5703-808F-41B2-8FCA-472312EA897A"); id != want {
		t.Fatalf("DiskID = %x, want %x", id, want)
	}
}

func TestSetDiskIDChangesOnlyTheIDAndTheHeader(t *testing.T) {
	path := sample(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, sequenceBefore := mustCurrentHeader(t, path)
	id := [16]byte{0x48, 0x4c, 0x41, 0x42, 1, 0, 0, 0, 0, 0, 0, 0, 0x01, 0x00, 0x00, 0x05}
	if err := SetDiskID(path, id); err != nil {
		t.Fatal(err)
	}
	if got, err := DiskID(path); err != nil || got != id {
		t.Fatalf("DiskID after SetDiskID = %x, %v; want %x", got, err, id)
	}
	header, sequence := mustCurrentHeader(t, path)
	if sequence != sequenceBefore+1 {
		t.Errorf("the header's sequence = %d, want %d", sequence, sequenceBefore+1)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("the file's size changed: %d to %d", len(before), len(after))
	}
	// the rest of the file stays as it was: the other header's place got
	// the new header, the page 83 item the ID
	offset, err := page83Offset(mustOpen(t, path))
	if err != nil {
		t.Fatal(err)
	}
	for i := range after {
		inHeaders := i >= headerOffset1 && i < headerOffset2+headerSize
		inID := int64(i) >= offset && int64(i) < offset+16
		if after[i] != before[i] && !inHeaders && !inID {
			t.Fatalf("byte %#x changed", i)
		}
	}
	if string(header[32:48]) != string(before[headerOffset2+32:headerOffset2+48]) {
		t.Error("the DataWriteGuid changed; a parent's children would no longer match it")
	}
}

func TestSetDiskIDRefusesWhatItCannotChange(t *testing.T) {
	notVHDX := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(notVHDX, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDiskID(notVHDX, [16]byte{}); !errors.Is(err, ErrFormat) {
		t.Errorf("SetDiskID of a plain file = %v, want ErrFormat", err)
	}
	// a log to replay: a LogGuid in the current header
	withLog := sample(t)
	header, _ := mustCurrentHeader(t, withLog)
	header[48] = 1
	binary.LittleEndian.PutUint32(header[4:8], 0)
	binary.LittleEndian.PutUint32(header[4:8], crcOf(header))
	file, err := os.OpenFile(withLog, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt(header, headerOffset2); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if err := SetDiskID(withLog, [16]byte{1}); !errors.Is(err, ErrFormat) {
		t.Errorf("SetDiskID of a VHDX with a log = %v, want ErrFormat", err)
	}
}

func TestWWNIsWhatTheGuestSaw(t *testing.T) {
	// phase 0 on Hyper-V: a VHDX with this DiskIdentifier showed the guest
	// this WWN
	id := guid("6649439B-3E5D-4FC3-B7F8-1AA4F3BC0F9A")
	if got, want := WWN(id), "0x600224809b4349665d3e1aa4f3bc0f9a"; got != want {
		t.Errorf("WWN = %s, want %s", got, want)
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func mustCurrentHeader(t *testing.T, path string) ([]byte, uint64) {
	t.Helper()
	header, _, err := currentHeader(mustOpen(t, path))
	if err != nil {
		t.Fatal(err)
	}
	return header, binary.LittleEndian.Uint64(header[8:16])
}

func crcOf(header []byte) uint32 {
	copied := append([]byte(nil), header...)
	binary.LittleEndian.PutUint32(copied[4:8], 0)
	return crc32.Checksum(copied, castagnoli)
}
