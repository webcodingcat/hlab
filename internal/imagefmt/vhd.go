package imagefmt

import (
	"encoding/binary"
	"fmt"
	"time"
)

// VHDFooterSize is the length of a VHD's footer.
const VHDFooterSize = 512

// vhdEpoch is where a VHD's timestamps count from.
var vhdEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// VHDFooter returns the footer that makes raw disk bytes of size a fixed
// VHD: written right after the bytes, Hyper-V reads the file as a disk
// (Microsoft's "Virtual Hard Disk Image Format Specification"), and
// Convert-VHD turns it into a dynamic VHDX. size must be a multiple of 512.
func VHDFooter(size uint64, created time.Time, id [16]byte) ([]byte, error) {
	if size == 0 || size%512 != 0 {
		return nil, fmt.Errorf("a VHD's size must be a positive multiple of 512, not %d", size)
	}
	footer := make([]byte, VHDFooterSize)
	be := binary.BigEndian
	copy(footer[0:], "conectix")
	be.PutUint32(footer[8:], 2)                      // features: reserved bit, always set
	be.PutUint32(footer[12:], 0x0001_0000)           // format version 1.0
	be.PutUint64(footer[16:], 0xffff_ffff_ffff_ffff) // data offset: none, a fixed disk
	be.PutUint32(footer[24:], uint32(created.Sub(vhdEpoch)/time.Second))
	copy(footer[28:], "hlab")              // creator application
	be.PutUint32(footer[32:], 0x0001_0000) // creator version
	copy(footer[36:], "Wi2k")              // creator host OS
	be.PutUint64(footer[40:], size)        // original size
	be.PutUint64(footer[48:], size)        // current size
	be.PutUint32(footer[56:], vhdGeometry(size/512))
	be.PutUint32(footer[60:], 2) // disk type: fixed
	copy(footer[68:], id[:])
	var sum uint32
	for _, b := range footer {
		sum += uint32(b)
	}
	be.PutUint32(footer[64:], ^sum)
	return footer, nil
}

// vhdGeometry returns the cylinders, heads and sectors per track of a disk
// of that many sectors, packed as the footer keeps them, by the
// specification's algorithm.
func vhdGeometry(sectors uint64) uint32 {
	sectors = min(sectors, 65535*16*255)
	var cylinderTimesHeads, heads, perTrack uint64
	if sectors >= 65535*16*63 {
		perTrack, heads = 255, 16
		cylinderTimesHeads = sectors / perTrack
	} else {
		perTrack = 17
		cylinderTimesHeads = sectors / perTrack
		heads = max((cylinderTimesHeads+1023)/1024, 4)
		if cylinderTimesHeads >= heads*1024 || heads > 16 {
			perTrack, heads = 31, 16
			cylinderTimesHeads = sectors / perTrack
		}
		if cylinderTimesHeads >= heads*1024 {
			perTrack, heads = 63, 16
			cylinderTimesHeads = sectors / perTrack
		}
	}
	cylinders := cylinderTimesHeads / heads
	return uint32(cylinders<<16 | heads<<8 | perTrack)
}
