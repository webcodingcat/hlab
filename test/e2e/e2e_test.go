//go:build e2e

package e2e

import "testing"

// TestE2E is the one end-to-end group; every subtest is named with its
// requirement ID only, so `-run 'TestE2E/SRV-01'` selects it and still gets
// the shared setup of TestMain and the lazily created project and server.
func TestE2E(t *testing.T) {
	// LIFE-01 stops and starts the cloud, so it runs alone before the
	// parallel rows.
	t.Run("LIFE-01", testLIFE01)

	t.Run("PROJ-05", parallel(testPROJ05))
	t.Run("API-01", parallel(testAPI01))
	t.Run("SRV-01", parallel(testSRV01))
	t.Run("SRV-03", parallel(testSRV03))
	t.Run("SRV-05", parallel(testSRV05))
	t.Run("SRV-16", parallel(testSRV16))
	t.Run("SRV-19", parallel(testSRV19))
}

func parallel(f func(t *testing.T)) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		f(t)
	}
}
