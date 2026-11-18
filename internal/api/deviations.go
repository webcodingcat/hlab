package api

// Deviation is one known difference between hlab and the Hetzner Cloud API.
type Deviation struct {
	Endpoint    string
	Description string
}

// Deviations lists the known differences from the Hetzner Cloud API; the
// user documentation names each of them.
func Deviations() []Deviation {
	return []Deviation{
		{
			Endpoint:    "POST /servers",
			Description: "the IPv6 /64 is assigned formally; the server gets no IPv6 traffic",
		},
		{
			Endpoint:    "POST /servers",
			Description: "the returned root password does not expire; Hetzner forces a change at the first login",
		},
		{
			Endpoint:    "POST /servers",
			Description: "resource_unavailable when the host lacks CPUs, RAM or disk beyond the configured reserve",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/shutdown",
			Description: "the action succeeds once the request is sent to the guest, not when the server is off",
		},
		{
			Endpoint:    "POST /servers",
			Description: "a server type whose disk is smaller than the image's disk_size answers 422 invalid_input on the field server_type",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/rebuild",
			Description: "takes a system image or a snapshot or backup of the project; an image larger than the server type's disk answers 422 invalid_input on the field image",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/create_image",
			Description: "the copy is crash-consistent, taken through a Hyper-V checkpoint while the server runs; image_size is the uncompressed disk file size",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/create_image",
			Description: "type backup on a server without backups enabled answers 422 invalid_input on the field type",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/enable_backup",
			Description: "backup_window is shown as 22-02 but unused; one daily backup per UTC day at the first hourly check while the cloud runs, none for days it did not run",
		},
		{
			Endpoint:    "DELETE /images/{id}",
			Description: "an image that is still being created answers 423 locked",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/enable_rescue",
			Description: "the Debian 13 rescue system boots from its own disk, not from memory; the server's disk stays /dev/sda",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/enable_rescue",
			Description: "rescue is disabled only on boots hlab drives (poweron, reboot, reset) or after 60 minutes; a reboot inside the guest keeps it enabled",
		},
		{
			Endpoint:    "GET /isos",
			Description: "lists the five ISOs of hlab's catalog with hlab's own IDs from 130001; no private ISOs",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/attach_iso",
			Description: "the first attach downloads the ISO inside the action; an unknown or deprecated ISO answers 422 invalid_input on the field iso; a second DVD drive holds the cloud-init seed",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/detach_iso",
			Description: "on a server without an ISO the action succeeds and changes nothing",
		},
		{
			Endpoint:    "POST /volumes",
			Description: "linux_device link, automount and pick-up of a grown volume work only through hlab guest hooks; without them a volume is a plain SCSI disk",
		},
		{
			Endpoint:    "POST /volumes",
			Description: "a location other than the server's answers 422 invalid_input on the field location; resource_unavailable when the volume does not fit beyond reserve_disk_gb",
		},
		{
			Endpoint:    "POST /volumes/{id}/actions/attach",
			Description: "at most 16 volumes per server, one more answers 422 invalid_input; another location answers 422 invalid_input on the field server or assignee",
		},
		{
			Endpoint:    "POST /volumes/{id}/actions/resize",
			Description: "the guest hook rescans the disk; a server without the hook sees the new size at the disk's next access",
		},
		{
			Endpoint:    "DELETE /volumes/{id}",
			Description: "an attached volume answers 422 invalid_input",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/reset_password",
			Description: "works through the hlab guest hook, not qemu-guest-agent; 409 conflict when the server is not running",
		},
		{
			Endpoint:    "GET /servers/{id}/metrics",
			Description: "samples every 10 s kept 24 h in memory, lost at cloud stop; disk.0 sums all disks of the VM",
		},
		{
			Endpoint:    "POST /servers/{id}/actions/request_console",
			Description: "answers unsupported_error: server console is not supported by hlab yet",
		},
		{
			Endpoint:    "http://169.254.169.254/hetzner/v1/metadata",
			Description: "no EC2 routes; public-keys and userdata in cloud-init's shape",
		},
	}
}
