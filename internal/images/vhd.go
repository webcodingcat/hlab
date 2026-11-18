package images

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lima-vm/go-qcow2reader"
	"github.com/lima-vm/go-qcow2reader/convert"
	"github.com/lima-vm/go-qcow2reader/image/qcow2"
)

const sectorSize = 512

// qcow2ToRaw writes the virtual disk of the qcow2 image src into the empty
// file dst, leaving unallocated ranges sparse.
func qcow2ToRaw(src io.ReaderAt, dst *os.File) error {
	img, err := qcow2reader.OpenWithType(src, qcow2.Type)
	if err != nil {
		return fmt.Errorf("open qcow2: %w", err)
	}
	defer func() { _ = img.Close() }()
	if err := img.Readable(); err != nil {
		return fmt.Errorf("qcow2: %w", err)
	}
	if err := dst.Truncate(img.Size()); err != nil {
		return err
	}
	if err := convert.Convert(dst, img, convert.Options{}); err != nil {
		return fmt.Errorf("qcow2 to raw: %w", err)
	}
	return nil
}

// appendVHDFooter turns the raw disk f into a fixed VHD: it pads the data
// to whole sectors and appends the 512-byte footer.
func appendVHDFooter(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size := (fi.Size() + sectorSize - 1) / sectorSize * sectorSize
	footer, err := vhdFooter(size, time.Now())
	if err != nil {
		return err
	}
	if _, err := f.WriteAt(footer, size); err != nil {
		return fmt.Errorf("write VHD footer: %w", err)
	}
	return nil
}

// vhdFooter returns the footer of a fixed VHD with size bytes of data.
func vhdFooter(size int64, now time.Time) ([]byte, error) {
	b := make([]byte, sectorSize)
	be := binary.BigEndian
	copy(b[0:8], "conectix")
	be.PutUint32(b[8:], 2)                   // features: reserved bit
	be.PutUint32(b[12:], 0x00010000)         // file format version
	be.PutUint64(b[16:], 0xFFFFFFFFFFFFFFFF) // data offset: none for fixed disks
	epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	be.PutUint32(b[24:], uint32(now.Sub(epoch)/time.Second)) //nolint:gosec // fits until 2136
	copy(b[28:32], "hlab")                                   // creator application
	be.PutUint32(b[32:], 0x00010000)                         // creator version
	copy(b[36:40], "Wi2k")                                   // creator host OS
	be.PutUint64(b[40:], uint64(size))                       //nolint:gosec // size is not negative
	be.PutUint64(b[48:], uint64(size))                       //nolint:gosec // size is not negative
	be.PutUint32(b[56:], chs(size/sectorSize))               // disk geometry
	be.PutUint32(b[60:], 2)                                  // disk type: fixed
	if _, err := rand.Read(b[68:84]); err != nil {
		return nil, err
	}
	var sum uint32
	for _, c := range b {
		sum += uint32(c)
	}
	be.PutUint32(b[64:], ^sum)
	return b, nil
}

// chs computes the disk geometry (cylinders 16 bit, heads 8 bit, sectors
// per track 8 bit) as the VHD specification defines it.
func chs(totalSectors int64) uint32 {
	if totalSectors > 65535*16*255 {
		totalSectors = 65535 * 16 * 255
	}
	var spt, heads, cth int64
	if totalSectors >= 65535*16*63 {
		spt, heads = 255, 16
		cth = totalSectors / spt
	} else {
		spt = 17
		cth = totalSectors / spt
		heads = max((cth+1023)/1024, 4)
		if cth >= heads*1024 || heads > 16 {
			spt, heads = 31, 16
			cth = totalSectors / spt
		}
		if cth >= heads*1024 {
			spt, heads = 63, 16
			cth = totalSectors / spt
		}
	}
	cyl := cth / heads
	return uint32(cyl)<<16 | uint32(heads)<<8 | uint32(spt) //nolint:gosec // bounded by the clamp above
}
