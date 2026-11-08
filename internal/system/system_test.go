package system_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/system"
)

// A secrets file of null, or none at all, gives an empty map that the
// caller can add to, not a nil map.
func TestReadSecretsGivesAMapToAddToForNullAndAMissingFile(t *testing.T) {
	dir := t.TempDir()
	null := filepath.Join(dir, "null.json")
	if err := os.WriteFile(null, []byte("null\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sys := system.New(fake.New().Platform())
	for _, path := range []string{null, filepath.Join(dir, "missing.json")} {
		secrets, err := sys.ReadSecrets(path)
		if err != nil {
			t.Fatal(err)
		}
		if secrets == nil || len(secrets) != 0 {
			t.Errorf("%s: got %v, want an empty map", filepath.Base(path), secrets)
		}
	}
}
