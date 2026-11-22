package apitest

import (
	_ "embed"
	"testing"

	"github.com/webcodingcat/hlab/internal/catalog"
)

//go:embed testdata/storage-box-types.json
var storageBoxTypes []byte

// StorageBoxTypes returns the recorded types of
// testdata/storage-box-types.json (bx11, bx21, bx31 in the real API shape,
// prices for fsn1, nbg1 and hel1; bx11 lacks nbg1).
func StorageBoxTypes(t testing.TB) []catalog.StorageBoxType {
	t.Helper()
	types, err := catalog.ParseStorageBoxTypes(storageBoxTypes)
	if err != nil {
		t.Fatalf("recorded storage box types: %v", err)
	}
	return types
}
