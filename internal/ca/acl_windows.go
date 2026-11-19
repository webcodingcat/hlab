//go:build windows

package ca

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// restrictKey gives the file at path a protected DACL with full control for
// SYSTEM only. A process that does not run as SYSTEM (tests, a manual start)
// also keeps full control for its own user, or it could not read the key
// again.
func restrictKey(path string) error {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("ca: SYSTEM SID: %w", err)
	}
	sids := []*windows.SID{system}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("ca: process user: %w", err)
	}
	if !user.User.Sid.Equals(system) {
		sids = append(sids, user.User.Sid)
	}
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(sids))
	for _, sid := range sids {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("ca: build ACL of %s: %w", keyFile, err)
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("ca: set ACL of %s: %w", keyFile, err)
	}
	return nil
}
