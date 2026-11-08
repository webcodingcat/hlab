package lab

import "context"

// ChangePrimaryIP is the steps of a Primary IP's assign and unassign: the
// plan has changed the records, the appliance gets the pictures with the
// server's new addresses, and then it waits Hetzner's time.
func (m *Machine) ChangePrimaryIP(ctx context.Context) error {
	if err := m.UpdatePictures(ctx); err != nil {
		return err
	}
	return m.lab.Sleep(ctx, PrimaryIPChange)
}
