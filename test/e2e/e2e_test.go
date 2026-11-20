//go:build e2e

package e2e

import "testing"

// TestE2E is the one end-to-end group; every subtest is named with its
// requirement ID only, so `-run 'TestE2E/SRV-01'` selects it and still gets
// the shared setup of TestMain and the lazily created project and server.
func TestE2E(t *testing.T) {
	// The server and snapshot SRV-12 shares go when all parallel rows end,
	// if a filter left out one of the rows using them.
	t.Cleanup(func() { releaseSharedSnapshot(t) })
	// The same for the servers and networks NET-02, NET-03, NET-04, HOST-01
	// and FIP-02 share.
	t.Cleanup(func() { releaseNetFixture(t) })
	// The same for the load balancer LB-02 to LB-05 and CERT-04 share.
	t.Cleanup(func() { releaseLBFixture(t) })
	// The e2e tray the UI rows share ends with the group.
	t.Cleanup(func() { releaseTray(t) })

	// LIFE-01 stops and starts the cloud, so it runs alone before the
	// parallel rows.
	t.Run("LIFE-01", testLIFE01)

	// LIFE-02, LIFE-03, LIFE-04 and LIFE-07 share one fixture and one stop
	// and start per stop behaviour, so they also run alone; their fixture
	// goes and settings.toml is restored before the parallel rows.
	t.Run("LIFE-02", testLIFE02)
	t.Run("LIFE-03", testLIFE03)
	t.Run("LIFE-04", testLIFE04)
	t.Run("LIFE-07", testLIFE07)
	releaseLifeFixture(t)

	// The UI rows drive the one tray window, and UI-01 stops and starts the
	// cloud, so they run alone after the LIFE rows.
	t.Run("UI-01", testUI01)
	t.Run("UI-03", testUI03)
	t.Run("UI-04", testUI04)
	t.Run("UI-05", testUI05)
	t.Run("UI-02", testUI02)
	t.Run("UI-06", testUI06)

	t.Run("PROJ-05", parallel(testPROJ05))
	t.Run("API-01", parallel(testAPI01))
	t.Run("SRV-01", parallel(testSRV01))
	t.Run("SRV-03", parallel(testSRV03))
	t.Run("SRV-05", parallel(testSRV05))
	t.Run("SRV-16", parallel(testSRV16))
	t.Run("SRV-19", parallel(testSRV19))
	t.Run("SRV-02", parallel(testSRV02))
	t.Run("SRV-07", parallel(testSRV07))
	t.Run("SRV-08", parallel(testSRV08))
	t.Run("SRV-08/own_snapshot", parallel(testSRV08OwnSnapshot))
	t.Run("SRV-09", parallel(testSRV09))
	t.Run("SRV-10", parallel(testSRV10))
	t.Run("SRV-21", parallel(testSRV21))
	t.Run("SRV-11", parallel(testSRV11))
	t.Run("SRV-12", parallel(testSRV12))
	t.Run("SRV-15", parallel(testSRV15))
	t.Run("VOL-02", parallel(testVOL02))
	t.Run("VOL-03", parallel(testVOL03))
	t.Run("VOL-04", parallel(testVOL04))
	t.Run("NET-02", parallel(testNET02))
	t.Run("NET-03", parallel(testNET03))
	t.Run("NET-04", parallel(testNET04))
	t.Run("HOST-01", parallel(testHOST01))
	t.Run("PIP-02", parallel(testPIP02))
	t.Run("FIP-02", parallel(testFIP02))
	t.Run("FW-02", parallel(testFW02))
	t.Run("FW-03", parallel(testFW03))
	t.Run("DNS-02", parallel(testDNS02))
	t.Run("DNS-03", parallel(testDNS03))
	t.Run("RDNS-02", parallel(testRDNS02))
	t.Run("META-01", parallel(testMETA01))
	t.Run("HOST-03", parallel(testHOST03))
	t.Run("CLI-02", parallel(testCLI02))
	t.Run("CLI-03", parallel(testCLI03))
	t.Run("CAT-03", parallel(testCAT03))
	t.Run("LB-02", parallel(testLB02))
	t.Run("LB-03", parallel(testLB03))
	t.Run("LB-04", parallel(testLB04))
	t.Run("LB-05", parallel(testLB05))
	t.Run("CERT-04", parallel(testCERT04))
}

func parallel(f func(t *testing.T)) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		f(t)
	}
}
