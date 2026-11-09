// Package vhdx reads and sets the page 83 disk ID of a VHDX file (MS-VHDX,
// "Page 83 Data"). Hyper-V shows a SCSI disk's page 83 to the guest as its
// WWN, so a disk ID that HLab sets names the disk inside a VM.
package vhdx

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// ErrFormat reports a file that is no VHDX this package can change.
var ErrFormat = errors.New("not a VHDX file")

// The layout of MS-VHDX: the file identifier, two headers and two region
// tables at fixed offsets; the metadata region holds a table of items.
const (
	headerOffset1     = 64 << 10
	headerOffset2     = 128 << 10
	headerSize        = 4 << 10
	regionTableOffset = 192 << 10
	regionTableSize   = 64 << 10
	metadataTableSize = 64 << 10
	entrySize         = 32
)

// The GUIDs MS-VHDX names: the metadata region and the page 83 item.
var (
	metadataRegion = guid("8B7CA206-4790-4B9A-B8FE-575F050F886E")
	page83Item     = guid("BECA12AB-B2E6-4523-93EF-C309E000C746")
)

// castagnoli is the CRC-32C that the headers and region tables carry.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// DiskID returns the page 83 disk ID of a VHDX, as its 16 bytes in the
// file.
func DiskID(path string) ([16]byte, error) {
	var id [16]byte
	file, err := os.Open(path)
	if err != nil {
		return id, err
	}
	defer func() { _ = file.Close() }()
	offset, err := page83Offset(file)
	if err != nil {
		return id, fmt.Errorf("%s: %w", path, err)
	}
	_, err = file.ReadAt(id[:], offset)
	return id, err
}

// SetDiskID sets the page 83 disk ID of a VHDX that no VM uses. As
// MS-VHDX asks of a writer, it first gives the file a new FileWriteGuid
// in its header. It refuses a file whose log holds entries to replay
// (a VHDX that was not closed cleanly): Hyper-V replays them on its next
// open and could undo the change. The 16 bytes go straight into the
// metadata, not through the log: a power cut in the middle of the write
// may leave a wrong ID, which the next attach writes anew.
func SetDiskID(path string, id [16]byte) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if err := newFileWriteGUID(file); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	offset, err := page83Offset(file)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if _, err := file.WriteAt(id[:], offset); err != nil {
		return err
	}
	return file.Sync()
}

// WWN returns the WWN that Hyper-V shows a guest for a disk ID, as the
// guest's udev writes it (ID_WWN_WITH_EXTENSION): NAA 6, Microsoft's OUI
// 002248, then bytes 0 to 5 and 10 to 15 of the ID as they are in the
// file; bytes 6 to 9 do not appear (measured with Hyper-V on Windows 11).
func WWN(id [16]byte) string {
	return "0x60022480" + hex.EncodeToString(id[0:6]) + hex.EncodeToString(id[10:16])
}

// page83Offset finds the page 83 item in the file's metadata region.
func page83Offset(file io.ReaderAt) (int64, error) {
	identifier := make([]byte, 8)
	if _, err := file.ReadAt(identifier, 0); err != nil || string(identifier) != "vhdxfile" {
		return 0, ErrFormat
	}
	table := make([]byte, regionTableSize)
	if _, err := file.ReadAt(table, regionTableOffset); err != nil {
		return 0, err
	}
	if string(table[0:4]) != "regi" || !checksumOK(table, 4) {
		return 0, fmt.Errorf("%w: a bad region table", ErrFormat)
	}
	count := binary.LittleEndian.Uint32(table[8:12])
	for i := range min(count, (regionTableSize-16)/entrySize) {
		entry := table[16+i*entrySize:]
		if bytes.Equal(entry[0:16], metadataRegion[:]) {
			return metadataItem(file, int64(binary.LittleEndian.Uint64(entry[16:24])))
		}
	}
	return 0, fmt.Errorf("%w: no metadata region", ErrFormat)
}

// metadataItem returns the file offset of the page 83 item of the
// metadata region at offset.
func metadataItem(file io.ReaderAt, region int64) (int64, error) {
	table := make([]byte, metadataTableSize)
	if _, err := file.ReadAt(table, region); err != nil {
		return 0, err
	}
	if string(table[0:8]) != "metadata" {
		return 0, fmt.Errorf("%w: a bad metadata table", ErrFormat)
	}
	count := int(binary.LittleEndian.Uint16(table[10:12]))
	for i := range min(count, metadataTableSize/entrySize-1) {
		entry := table[entrySize+i*entrySize:]
		if bytes.Equal(entry[0:16], page83Item[:]) {
			if binary.LittleEndian.Uint32(entry[20:24]) != 16 {
				return 0, fmt.Errorf("%w: page 83 is not 16 bytes", ErrFormat)
			}
			return region + int64(binary.LittleEndian.Uint32(entry[16:20])), nil
		}
	}
	return 0, fmt.Errorf("%w: no page 83 item", ErrFormat)
}

// newFileWriteGUID writes the current header anew, with the next
// sequence number and a new FileWriteGuid, into the other header's place;
// it then is the current one. It refuses a log that is not empty.
func newFileWriteGUID(file *os.File) error {
	current, other, err := currentHeader(file)
	if err != nil {
		return err
	}
	if !bytes.Equal(current[48:64], make([]byte, 16)) {
		return fmt.Errorf("%w: its log holds entries to replay", ErrFormat)
	}
	sequence := binary.LittleEndian.Uint64(current[8:16])
	binary.LittleEndian.PutUint64(current[8:16], sequence+1)
	if _, err := rand.Read(current[16:32]); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(current[4:8], 0)
	binary.LittleEndian.PutUint32(current[4:8], crc32.Checksum(current, castagnoli))
	if _, err := file.WriteAt(current, other); err != nil {
		return err
	}
	return file.Sync()
}

// currentHeader returns the valid header with the higher sequence number
// and the offset of the other one.
func currentHeader(file io.ReaderAt) (header []byte, otherOffset int64, err error) {
	var sequence uint64
	for _, offset := range []int64{headerOffset1, headerOffset2} {
		candidate := make([]byte, headerSize)
		if _, err := file.ReadAt(candidate, offset); err != nil {
			return nil, 0, err
		}
		if string(candidate[0:4]) != "head" || !checksumOK(candidate, 4) {
			continue
		}
		if next := binary.LittleEndian.Uint64(candidate[8:16]); header == nil || next > sequence {
			header, sequence = candidate, next
			otherOffset = headerOffset1 + headerOffset2 - offset
		}
	}
	if header == nil {
		return nil, 0, fmt.Errorf("%w: no valid header", ErrFormat)
	}
	return header, otherOffset, nil
}

// checksumOK checks a structure's CRC-32C, which is stored at at and
// computed with that field as zero.
func checksumOK(data []byte, at int) bool {
	stored := binary.LittleEndian.Uint32(data[at : at+4])
	copied := bytes.Clone(data)
	binary.LittleEndian.PutUint32(copied[at:at+4], 0)
	return crc32.Checksum(copied, castagnoli) == stored
}

// guid returns a GUID as MS-VHDX stores it: the first three groups little
// endian, the rest as written.
func guid(text string) [16]byte {
	var out [16]byte
	raw, err := hex.DecodeString(text[0:8] + text[9:13] + text[14:18] + text[19:23] + text[24:36])
	if err != nil || len(raw) != 16 {
		panic("vhdx: bad GUID " + text)
	}
	copy(out[:], raw)
	reverse := func(part []byte) {
		for i, j := 0, len(part)-1; i < j; i, j = i+1, j-1 {
			part[i], part[j] = part[j], part[i]
		}
	}
	reverse(out[0:4])
	reverse(out[4:6])
	reverse(out[6:8])
	return out
}
