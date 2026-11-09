package lab

import "context"

// ChangePrimaryIP is the steps of a Primary IP's assign and unassign: the
// plan has changed the records, the appliance gets the pictures with the
// server's new addresses.
func (m *Machine) ChangePrimaryIP(ctx context.Context) error {
	return m.UpdatePictures(ctx)
}
