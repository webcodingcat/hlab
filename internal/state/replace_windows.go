package state

import (
	"os"
	"syscall"
	"unsafe"
)

// MoveFileExW and its flags (winbase.h). os.Rename uses MoveFileExW without
// MOVEFILE_WRITE_THROUGH, which returns before the rename is on disk.
var moveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

// replaceFile moves from over to, and returns once the move is on disk.
func replaceFile(from, to string) error {
	fromPtr, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	done, _, err := moveFileEx.Call(
		uintptr(unsafe.Pointer(fromPtr)),
		uintptr(unsafe.Pointer(toPtr)),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if done == 0 {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
