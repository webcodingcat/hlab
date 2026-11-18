//go:build windows

package windows

import (
	"context"
	"errors"

	xwin "golang.org/x/sys/windows"

	"github.com/webcodingcat/hlab/internal/profile"
)

// otherServiceRunning reports whether the service of the other profile
// runs, with the minimal rights a non-admin user has.
func otherServiceRunning(context.Context) (bool, error) {
	m, err := xwin.OpenSCManager(nil, nil, xwin.SC_MANAGER_CONNECT)
	if err != nil {
		return false, err
	}
	defer func() { _ = xwin.CloseServiceHandle(m) }()
	name, err := xwin.UTF16PtrFromString(profile.Other().ServiceName)
	if err != nil {
		return false, err
	}
	s, err := xwin.OpenService(m, name, xwin.SERVICE_QUERY_STATUS)
	if errors.Is(err, xwin.ERROR_SERVICE_DOES_NOT_EXIST) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = xwin.CloseServiceHandle(s) }()
	var st xwin.SERVICE_STATUS
	if err := xwin.QueryServiceStatus(s, &st); err != nil {
		return false, err
	}
	return st.CurrentState != xwin.SERVICE_STOPPED, nil
}
