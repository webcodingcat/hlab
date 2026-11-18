//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// testCLI03 holds the Ansible rows of CLI-03, one subtest per module.
func testCLI03(t *testing.T) {
	t.Run("server_info", parallel(testCLI03ServerInfo))
}

// testCLI03ServerInfo: hetzner.hcloud.server_info in the selected distro with
// only the endpoint changed lists the project's servers without a filter
// and finds the shared server by name.
func testCLI03ServerInfo(t *testing.T) {
	_, srv := sharedServer(t)
	e, w := sharedAnsible(t)

	playbook := fmt.Sprintf(`- hosts: localhost
  connection: local
  gather_facts: false
  vars:
    ansible_python_interpreter: "{{ ansible_playbook_python }}"
    shared_name: %q
    shared_id: %d
  tasks:
    - name: list all servers
      hetzner.hcloud.server_info:
      register: all_servers
    - name: find the shared server by name
      hetzner.hcloud.server_info:
        name: "{{ shared_name }}"
      register: by_name
    - name: check the answers
      ansible.builtin.assert:
        that:
          - shared_name in (all_servers.hcloud_server_info | map(attribute='name') | list)
          - by_name.hcloud_server_info | length == 1
          - by_name.hcloud_server_info[0].name == shared_name
          - by_name.hcloud_server_info[0].id | int == shared_id
          - by_name.hcloud_server_info[0].ipv4_address == %q
`, srv.Name, srv.ID, srv.IPv4)

	r := e.playbook(t, w, "cli03-server-info", playbook)
	if !strings.Contains(r.Stdout, "failed=0") {
		t.Errorf("playbook did not finish without failures:\n%s", r)
	}
}
