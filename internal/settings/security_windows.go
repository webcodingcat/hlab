//go:build windows

package settings

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// copySecurity gives dst the DACL of src, with its protection, so that a
// rename of dst over src keeps the access rules of src (settings.toml grants
// Interactive Users modify). A missing src leaves dst as it is.
func copySecurity(src, dst string) error {
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	sd, err := windows.GetNamedSecurityInfo(src, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		info = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.SetNamedSecurityInfo(dst, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil)
}
