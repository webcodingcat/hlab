// Package imagefmt reads and writes disk image formats without outside
// tools: the system images come as qcow2, and Hyper-V takes a VHD or VHDX.
// Its functions take readers and writers, so the tests need no files.
package imagefmt

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// qcow2Magic starts every qcow2 file: "QFI" and 0xfb.
var qcow2Magic = []byte{'Q', 'F', 'I', 0xfb}

// The bits of qcow2's L1 and L2 table entries.
const (
	offsetMask     = 0x00ff_ffff_ffff_fe00 // bits 9–55: a host offset
	l2Compressed   = 1 << 62
	l2ReadsAsZeros = 1 // version 3: the cluster reads as zeros
)

// ErrUnsupported reports a qcow2 file that uses a feature the reader does
// not have: a backing file, encryption, an external data file, extended L2
// entries or a compression other than zlib. The official cloud images use
// none of them.
var ErrUnsupported = errors.New("unsupported qcow2 feature")

// qcow2Header is the part of the header the reader needs.
type qcow2Header struct {
	version       uint32
	clusterBits   uint32
	size          uint64 // the virtual disk's size in bytes
	l1Size        uint32
	l1TableOffset uint64
}

// readQCOW2Header reads and checks the header.
func readQCOW2Header(src io.ReaderAt) (qcow2Header, error) {
	raw := make([]byte, 105)
	if n, err := src.ReadAt(raw, 0); n < 72 {
		return qcow2Header{}, fmt.Errorf("reading the qcow2 header: %w", err)
	}
	if !bytes.Equal(raw[:4], qcow2Magic) {
		return qcow2Header{}, errors.New("not a qcow2 file")
	}
	be := binary.BigEndian
	header := qcow2Header{
		version:       be.Uint32(raw[4:]),
		clusterBits:   be.Uint32(raw[20:]),
		size:          be.Uint64(raw[24:]),
		l1Size:        be.Uint32(raw[36:]),
		l1TableOffset: be.Uint64(raw[40:]),
	}
	switch {
	case header.version != 2 && header.version != 3:
		return qcow2Header{}, fmt.Errorf("%w: version %d", ErrUnsupported, header.version)
	case be.Uint64(raw[8:]) != 0:
		return qcow2Header{}, fmt.Errorf("%w: a backing file", ErrUnsupported)
	case be.Uint32(raw[32:]) != 0:
		return qcow2Header{}, fmt.Errorf("%w: encryption", ErrUnsupported)
	case header.clusterBits < 9 || header.clusterBits > 21:
		return qcow2Header{}, fmt.Errorf("qcow2 cluster bits %d out of range", header.clusterBits)
	}
	if header.version == 3 {
		// incompatible features: bit 0 (dirty) changes nothing for a
		// reader; the others (corrupt, external data, compression type,
		// extended L2) do
		if features := be.Uint64(raw[72:]); features&^1 != 0 {
			return qcow2Header{}, fmt.Errorf("%w: incompatible features %#x", ErrUnsupported, features)
		}
	}
	return header, nil
}

// QCOW2Size returns the virtual size in bytes of a qcow2 disk.
func QCOW2Size(src io.ReaderAt) (uint64, error) {
	header, err := readQCOW2Header(src)
	return header.size, err
}

// ConvertQCOW2 writes the disk of a qcow2 file to dst as raw bytes, from
// offset 0 to its virtual size. Clusters that are not allocated or read as
// zeros are skipped, so dst must read as zeros where nothing is written: a
// new file, truncated to the size (WriteVHD does that).
func ConvertQCOW2(src io.ReaderAt, dst io.WriterAt) error {
	header, err := readQCOW2Header(src)
	if err != nil {
		return err
	}
	clusterSize := uint64(1) << header.clusterBits
	entriesPerL2 := clusterSize / 8
	clusters := (header.size + clusterSize - 1) / clusterSize
	l1, err := readTable(src, header.l1TableOffset, uint64(header.l1Size))
	if err != nil {
		return fmt.Errorf("reading the L1 table: %w", err)
	}
	cluster := make([]byte, clusterSize)
	for l1Index, l1Entry := range l1 {
		l2Offset := l1Entry & offsetMask
		if l2Offset == 0 {
			continue // no L2 table: every cluster reads as zeros
		}
		l2, err := readTable(src, l2Offset, entriesPerL2)
		if err != nil {
			return fmt.Errorf("reading an L2 table: %w", err)
		}
		for l2Index, l2Entry := range l2 {
			number := uint64(l1Index)*entriesPerL2 + uint64(l2Index)
			if number >= clusters {
				break
			}
			present, err := readCluster(src, header, l2Entry, cluster)
			if err != nil {
				return fmt.Errorf("reading cluster %d: %w", number, err)
			}
			if !present {
				continue
			}
			length := min(clusterSize, header.size-number*clusterSize)
			if _, err := dst.WriteAt(cluster[:length], int64(number*clusterSize)); err != nil {
				return err
			}
		}
	}
	return nil
}

// readTable reads a table of big-endian 64-bit entries.
func readTable(src io.ReaderAt, offset, entries uint64) ([]uint64, error) {
	raw := make([]byte, entries*8)
	if _, err := src.ReadAt(raw, int64(offset)); err != nil {
		return nil, err
	}
	table := make([]uint64, entries)
	for i := range table {
		table[i] = binary.BigEndian.Uint64(raw[i*8:])
	}
	return table, nil
}

// readCluster reads the cluster of an L2 entry into cluster; present is
// false for a cluster that reads as zeros.
func readCluster(src io.ReaderAt, header qcow2Header, entry uint64, cluster []byte) (present bool, err error) {
	if entry&l2Compressed != 0 {
		return true, readCompressed(src, header, entry, cluster)
	}
	offset := entry & offsetMask
	if offset == 0 || (header.version == 3 && entry&l2ReadsAsZeros != 0) {
		return false, nil
	}
	if _, err := src.ReadAt(cluster, int64(offset)); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return true, nil
}

// readCompressed inflates a compressed cluster: raw deflate data that
// starts at a host offset and spans a number of 512-byte sectors, as the
// qcow2 specification lays the entry out.
func readCompressed(src io.ReaderAt, header qcow2Header, entry uint64, cluster []byte) error {
	offsetBits := 62 - (header.clusterBits - 8)
	offset := entry & (1<<offsetBits - 1)
	sectors := (entry>>offsetBits)&(1<<(62-offsetBits)-1) + 1
	length := sectors*512 - offset%512
	compressed := make([]byte, length)
	// the last cluster of the file may end before the sectors do
	if n, err := src.ReadAt(compressed, int64(offset)); err != nil && (!errors.Is(err, io.EOF) || n == 0) {
		return err
	}
	inflater := flate.NewReader(bytes.NewReader(compressed))
	defer func() { _ = inflater.Close() }() // closing a reader of memory cannot fail
	if _, err := io.ReadFull(inflater, cluster); err != nil {
		return fmt.Errorf("inflating: %w", err)
	}
	return nil
}
