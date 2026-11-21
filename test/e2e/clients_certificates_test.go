//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "certificates", Run: testCLI01Certificates})
	registerRow(clientRow{Req: "CLI-02", Name: "certificates", Run: testCLI02Certificates})
	registerRow(clientRow{Req: "CLI-03", Name: "certificates", Run: testCLI03Certificates})
}

// selfSigned makes a self-signed certificate for host and writes it and its
// key as PEM into dir; it returns the files and their content.
func selfSigned(t *testing.T, dir, host string) (certFile, keyFile, certPEM, keyPEM string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, []byte(certPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte(keyPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, certPEM, keyPEM
}

// certDetail is what the certificate rows check of GET /certificates/{id}.
type certDetail struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Labels      map[string]string `json:"labels"`
	DomainNames []string          `json:"domain_names"`
	Fingerprint string            `json:"fingerprint"`
	Status      *struct {
		Issuance string `json:"issuance"`
	} `json:"status"`
}

func (e *env) certInfo(t *testing.T, id int64) certDetail {
	t.Helper()
	var body struct {
		Certificate certDetail `json:"certificate"`
	}
	e.getOK(t, fmt.Sprintf("/certificates/%d", id), &body)
	return body.Certificate
}

// certByName is the one certificate with the name.
func (e *env) certByName(t *testing.T, name string) certDetail {
	t.Helper()
	var body struct {
		Certificates []certDetail `json:"certificates"`
	}
	e.getOK(t, "/certificates?name="+url.QueryEscape(name), &body)
	if len(body.Certificates) != 1 {
		t.Fatalf("GET /certificates?name=%s: %d certificates, want 1", name, len(body.Certificates))
	}
	return body.Certificates[0]
}

// zoneByName is the ID of the one zone with the name.
func (e *env) zoneByName(t *testing.T, name string) int64 {
	t.Helper()
	var body struct {
		Zones []named `json:"zones"`
	}
	e.getOK(t, "/zones?name="+url.QueryEscape(name), &body)
	if len(body.Zones) != 1 {
		t.Fatalf("GET /zones?name=%s: %d zones, want 1", name, len(body.Zones))
	}
	return body.Zones[0].ID
}

// waitIssued waits until the managed certificate's issuance is completed.
func (e *env) waitIssued(t *testing.T, id int64) {
	t.Helper()
	var last string
	if err := waitFor(5*time.Minute, 2*time.Second, func() (bool, error) {
		c := e.certInfo(t, id)
		if c.Status != nil {
			last = c.Status.Issuance
		}
		if last == "failed" {
			return false, fmt.Errorf("issuance of certificate %d failed", id)
		}
		return last == "completed", nil
	}); err != nil {
		t.Fatalf("managed certificate %d (issuance %q): %v", id, last, err)
	}
}

// checkCerts checks the uploaded and the managed certificate of a row
// through the API.
func checkCerts(t *testing.T, e *env, up, man certDetail, upHost, manHost string, labels map[string]string) {
	t.Helper()
	if up.Type != "uploaded" || !slices.Equal(up.DomainNames, []string{upHost}) || up.Fingerprint == "" || !maps.Equal(up.Labels, labels) {
		t.Errorf("uploaded certificate %+v, want type uploaded for %s with a fingerprint and labels %v", up, upHost, labels)
	}
	if man.Type != "managed" || !slices.Equal(man.DomainNames, []string{manHost}) || !maps.Equal(man.Labels, labels) {
		t.Errorf("managed certificate %+v, want type managed for %s with labels %v", man, manHost, labels)
	}
	e.waitIssued(t, man.ID)
}

// testCLI01Certificates: hcloud creates an uploaded and a managed
// certificate, describes, lists, updates, labels, retries and deletes them.
func testCLI01Certificates(t *testing.T, c *rowCtx) {
	e := c.E
	ctx := context.Background()
	zone := zoneName("hcloud-cert")
	upName, manName := resName("hcloud", "certificates-up"), resName("hcloud", "certificates-managed")
	upHost, manHost := "up."+zone, "www."+zone
	t.Cleanup(func() {
		for _, n := range []string{upName, manName, upName + "-renamed", manName + "-renamed"} {
			_, _ = e.hcloudRun(ctx, 2*time.Minute, "certificate", "delete", n)
		}
		_, _ = e.hcloudRun(ctx, 5*time.Minute, "zone", "delete", zone)
	})
	certFile, keyFile, _, _ := selfSigned(t, t.TempDir(), upHost)

	e.hcloudOK(t, "zone", "create", "--name", zone)
	zoneID := e.zoneByName(t, zone)
	e.hcloudOK(t, "certificate", "create", "--name", upName, "--type", "uploaded", "--cert-file", certFile, "--key-file", keyFile, "--label", "row=cli-01-certificates")
	e.hcloudOK(t, "certificate", "create", "--name", manName, "--type", "managed", "--domain", manHost, "--label", "row=cli-01-certificates")
	up, man := e.certByName(t, upName), e.certByName(t, manName)
	checkCerts(t, e, up, man, upHost, manHost, map[string]string{"row": "cli-01-certificates"})

	var listed []named
	e.hcloudJSON(t, &listed, "certificate", "list", "-l", "row=cli-01-certificates")
	if got, want := namedOf(listed), map[int64]string{up.ID: upName, man.ID: manName}; !maps.Equal(got, want) {
		t.Errorf("hcloud certificate list -l row=cli-01-certificates: %v, want %v", got, want)
	}
	for _, cd := range []certDetail{up, man} {
		var described certDetail
		e.hcloudJSON(t, &described, "certificate", "describe", cd.Name)
		if described.ID != cd.ID || described.Type != cd.Type || described.Fingerprint != e.certInfo(t, cd.ID).Fingerprint {
			t.Errorf("hcloud certificate describe %s: %+v, the API has %+v", cd.Name, described, e.certInfo(t, cd.ID))
		}
	}

	for _, cd := range []certDetail{up, man} {
		e.hcloudOK(t, "certificate", "update", "--name", cd.Name+"-renamed", cd.Name)
		e.hcloudOK(t, "certificate", "add-label", cd.Name+"-renamed", "stage=two")
		e.hcloudOK(t, "certificate", "remove-label", cd.Name+"-renamed", "row")
		got := e.certInfo(t, cd.ID)
		if want := map[string]string{"stage": "two"}; got.Name != cd.Name+"-renamed" || !maps.Equal(got.Labels, want) {
			t.Errorf("updated certificate %q with labels %v, want %q with %v", got.Name, got.Labels, cd.Name+"-renamed", want)
		}
	}

	before := certActions(t, e, man.ID)
	e.hcloudOK(t, "certificate", "retry", manName+"-renamed")
	if after := certActions(t, e, man.ID); after <= before {
		t.Errorf("certificate retry added no action (%d before, %d after)", before, after)
	}
	e.waitIssued(t, man.ID)

	e.hcloudOK(t, "certificate", "delete", upName+"-renamed", manName+"-renamed")
	e.hcloudOK(t, "zone", "delete", zone)
	assertGoneIDs(t, e, "/certificates", "certificates", up.ID, man.ID)
	assertGoneIDs(t, e, "/zones", "zones", zoneID)
}

// certActions counts the actions of a certificate.
func certActions(t *testing.T, e *env, id int64) int {
	t.Helper()
	var body struct {
		Actions []json.RawMessage `json:"actions"`
	}
	e.getOK(t, fmt.Sprintf("/certificates/%d/actions?per_page=50", id), &body)
	return len(body.Actions)
}

// testCLI02Certificates: OpenTofu manages hcloud_uploaded_certificate and
// hcloud_managed_certificate, updates them in place, reads
// hcloud_certificate and hcloud_certificates, and destroys them.
func testCLI02Certificates(t *testing.T, _ *rowCtx) {
	e := sharedTofu(t)
	d := e.newTofuDir(t, "certificates")
	zone := zoneName("tofu-cert")
	upHost, manHost := "up."+zone, "www."+zone
	selfSigned(t, d.dir, upHost)
	main := func(suffix, stage string) string {
		return fmt.Sprintf(`resource "hcloud_zone" "z" {
  name = %[1]q
  mode = "primary"
}

resource "hcloud_uploaded_certificate" "up" {
  name        = %[2]q
  certificate = file("${path.module}/cert.pem")
  private_key = file("${path.module}/key.pem")
  labels = {
    row   = "cli-02-certificates"
    stage = %[4]q
  }
}

resource "hcloud_managed_certificate" "man" {
  name         = %[3]q
  domain_names = ["www.${hcloud_zone.z.name}"]
  labels = {
    row   = "cli-02-certificates"
    stage = %[4]q
  }
}

data "hcloud_certificate" "up" {
  id = hcloud_uploaded_certificate.up.id
}

data "hcloud_certificates" "by_label" {
  with_selector = "row=cli-02-certificates"
  depends_on    = [hcloud_uploaded_certificate.up, hcloud_managed_certificate.man]
}

output "zone" {
  value = hcloud_zone.z.id
}

output "up" {
  value = hcloud_uploaded_certificate.up.id
}

output "man" {
  value = hcloud_managed_certificate.man.id
}

output "ds_fingerprint" {
  value = data.hcloud_certificate.up.fingerprint
}

output "ds_ids" {
  value = jsonencode(sort([for c in data.hcloud_certificates.by_label.certificates : tostring(c.id)]))
}
`, zone, resName("tofu", "certificates-up"+suffix), resName("tofu", "certificates-managed"+suffix), stage)
	}
	d.run(t, "init", "-no-color")

	d.writeMain(t, main("", "one"))
	d.run(t, "apply", "-auto-approve", "-no-color")
	zoneID := parseID(t, d.output(t, "zone"))
	upID, manID := parseID(t, d.output(t, "up")), parseID(t, d.output(t, "man"))
	t.Cleanup(func() {
		for _, id := range []int64{upID, manID} {
			_, _, _ = e.apiDo(context.Background(), http.MethodDelete, fmt.Sprintf("/certificates/%d", id), nil, nil)
		}
		_ = e.deleteZone(zoneID)
	})
	up, man := e.certInfo(t, upID), e.certInfo(t, manID)
	checkCerts(t, e, up, man, upHost, manHost, map[string]string{"row": "cli-02-certificates", "stage": "one"})
	if fp := d.output(t, "ds_fingerprint"); fp != up.Fingerprint {
		t.Errorf("data hcloud_certificate: fingerprint %q, the API has %q", fp, up.Fingerprint)
	}
	ids := []string{strconv.FormatInt(upID, 10), strconv.FormatInt(manID, 10)}
	slices.Sort(ids)
	wantIDs, _ := json.Marshal(ids)
	if got := d.output(t, "ds_ids"); got != string(wantIDs) {
		t.Errorf("data hcloud_certificates with row=cli-02-certificates: IDs %s, want %s", got, wantIDs)
	}

	d.writeMain(t, main("-renamed", "two"))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if want := "0 added, 2 changed, 0 destroyed"; !strings.Contains(r.Stdout, want) {
		t.Errorf("second apply is no in-place update of both certificates (want %q):\n%s", want, r.Stdout)
	}
	labels := map[string]string{"row": "cli-02-certificates", "stage": "two"}
	for id, name := range map[int64]string{upID: resName("tofu", "certificates-up-renamed"), manID: resName("tofu", "certificates-managed-renamed")} {
		if got := e.certInfo(t, id); got.Name != name || !maps.Equal(got.Labels, labels) {
			t.Errorf("updated certificate %d: %q with labels %v, want %q with %v", id, got.Name, got.Labels, name, labels)
		}
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/certificates", "certificates", upID, manID)
	assertGoneIDs(t, e, "/zones", "zones", zoneID)
}

// testCLI03Certificates: certificate creates an uploaded and a managed
// certificate, updates and deletes them; certificate_info finds them.
func testCLI03Certificates(t *testing.T, _ *rowCtx) {
	e, w := sharedAnsible(t)
	zone := zoneName("ansible-cert")
	upName, manName := resName("ansible", "certificates-up"), resName("ansible", "certificates-managed")
	upHost, manHost := "up."+zone, "www."+zone
	_, _, certPEM, keyPEM := selfSigned(t, t.TempDir(), upHost)
	t.Cleanup(func() {
		for _, n := range []string{upName, manName} {
			_, _ = e.hcloudRun(context.Background(), 2*time.Minute, "certificate", "delete", n)
		}
		_, _ = e.hcloudRun(context.Background(), 5*time.Minute, "zone", "delete", zone)
	})
	row := map[string]any{"zone": zone, "up": upName, "man": manName, "domain": manHost, "cert": certPEM, "key": keyPEM}
	certs := func(stage string) string {
		return `    - name: uploaded certificate
      hetzner.hcloud.certificate:
        name: "{{ row.up }}"
        type: uploaded
        certificate: "{{ row.cert }}"
        private_key: "{{ row.key }}"
        labels:
          row: cli-03-certificates
          stage: ` + stage + `
        state: present
    - name: managed certificate
      hetzner.hcloud.certificate:
        name: "{{ row.man }}"
        type: managed
        domain_names: ["{{ row.domain }}"]
        labels:
          row: cli-03-certificates
          stage: ` + stage + `
        state: present
`
	}

	e.ansibleOK(t, w, "cli03-certificates-create", localPlay(t, row, `    - name: zone
      hetzner.hcloud.zone:
        name: "{{ row.zone }}"
        mode: primary
        state: present
`+certs("one")))
	zoneID := e.zoneByName(t, zone)
	up, man := e.certByName(t, upName), e.certByName(t, manName)
	checkCerts(t, e, up, man, upHost, manHost, map[string]string{"row": "cli-03-certificates", "stage": "one"})

	row["up_id"], row["man_id"], row["fingerprint"] = up.ID, man.ID, up.Fingerprint
	e.ansibleOK(t, w, "cli03-certificates-update", localPlay(t, row, certs("two")+`    - name: uploaded by name
      hetzner.hcloud.certificate_info:
        name: "{{ row.up }}"
      register: by_name
    - name: both by label selector
      hetzner.hcloud.certificate_info:
        label_selector: row=cli-03-certificates
      register: by_label
    - name: check the answers
      ansible.builtin.assert:
        that:
          - by_name.hcloud_certificate_info | length == 1
          - by_name.hcloud_certificate_info[0].id | int == row.up_id
          - by_name.hcloud_certificate_info[0].fingerprint == row.fingerprint
          - (by_label.hcloud_certificate_info | map(attribute='id') | map('int') | sort | list) == ([row.up_id, row.man_id] | sort | list)
`))
	labels := map[string]string{"row": "cli-03-certificates", "stage": "two"}
	for _, id := range []int64{up.ID, man.ID} {
		if got := e.certInfo(t, id); !maps.Equal(got.Labels, labels) {
			t.Errorf("updated certificate %d labels %v, want %v", id, got.Labels, labels)
		}
	}

	e.ansibleOK(t, w, "cli03-certificates-absent", localPlay(t, row, `    - name: uploaded absent
      hetzner.hcloud.certificate:
        name: "{{ row.up }}"
        state: absent
    - name: managed absent
      hetzner.hcloud.certificate:
        name: "{{ row.man }}"
        state: absent
    - name: zone absent
      hetzner.hcloud.zone:
        name: "{{ row.zone }}"
        state: absent
`))
	assertGoneIDs(t, e, "/certificates", "certificates", up.ID, man.ID)
	assertGoneIDs(t, e, "/zones", "zones", zoneID)
}
