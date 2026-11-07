package internal_test

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const module = "github.com/webcodingcat/hlab/"

// allowedImports is the "may import" column of DEVELOPING.md, Layers: for each
// package below internal/, the packages of this module it may import. Packages
// that do not exist yet are listed too, so the rule holds from their first
// line. The commands under cmd/ may import anything.
var allowedImports = map[string][]string{
	"internal/tray":                    {"internal/lab"},
	"internal/cli":                     {"internal/client", "internal/config"},
	"internal/client":                  {"internal/labapi"},
	"internal/api/cloud":               {"internal/lab", "internal/web", "internal/state", "internal/catalog", "internal/schema"},
	"internal/api/hetzner":             {"internal/lab", "internal/web", "internal/state", "internal/catalog", "internal/schema"},
	"internal/api/control":             {"internal/lab", "internal/web", "internal/state", "internal/labapi"},
	"internal/labapi":                  {},
	"internal/webui":                   {"internal/web"},
	"internal/web":                     {"internal/state"},
	"internal/lab":                     {"internal/state", "internal/picture", "internal/system", "internal/catalog", "internal/config"},
	"internal/state":                   {},
	"internal/picture":                 {"internal/state", "internal/agentproto", "internal/catalog"},
	"internal/seed":                    {},
	"internal/system":                  {"internal/platform", "internal/agentproto", "internal/vhdx", "internal/imagefmt"},
	"internal/platform":                {},
	"internal/platform/windows":        {"internal/platform", "internal/platform/windows/pshost"},
	"internal/platform/windows/pshost": {},
	"internal/platform/linux":          {"internal/platform"},
	"internal/platform/fake":           {"internal/platform"},
	"internal/platform/contract":       {"internal/platform"},
	"internal/agentproto":              {},
	"internal/agent":                   {"internal/agentproto"},
	"internal/vhdx":                    {},
	"internal/imagefmt":                {},
	"internal/catalog":                 {},
	"internal/config":                  {},
	"internal/schema":                  {},
	"internal/spectest":                {},
}

func TestEveryAllowedImportIsAPackageOfTheTable(t *testing.T) {
	for pkg, allowed := range allowedImports {
		for _, imported := range allowed {
			if _, known := allowedImports[imported]; !known {
				t.Errorf("%s may import %s, which is not in the table", pkg, imported)
			}
		}
	}
}

func TestPackagesImportOnlyWhatTheirLayerAllows(t *testing.T) {
	// go/packages asks the go command; a test binary built for Windows and run
	// through WSL interop finds none, and CI's Windows job runs this test anyway.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the go command is not on PATH")
	}
	// both targets, so files for the other system are checked as well
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			// production code only: tests may import fakes and test helpers
			cfg := &packages.Config{
				Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports,
				Dir:  "..",
				Env:  append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0"),
			}
			loaded, err := packages.Load(cfg, "./...")
			if err != nil {
				t.Fatal(err)
			}
			if packages.PrintErrors(loaded) > 0 {
				t.Fatal("the packages do not load")
			}
			for _, pkg := range loaded {
				checkImports(t, pkg)
			}
		})
	}
}

// checkImports reports every import of a package of this module that the
// table does not allow, and a package under internal/ that is not in it.
func checkImports(t *testing.T, pkg *packages.Package) {
	t.Helper()
	name := strings.TrimPrefix(pkg.PkgPath, module)
	if strings.HasPrefix(name, "cmd/") || len(pkg.GoFiles) == 0 {
		return // commands wire everything; test-only packages import nothing
	}
	allowed, known := allowedImports[name]
	if !known {
		t.Errorf("%s is not in the layer table of layers_test.go and DEVELOPING.md", name)
		return
	}
	for path := range pkg.Imports {
		imported, ours := strings.CutPrefix(path, module)
		if ours && !slices.Contains(allowed, imported) {
			t.Errorf("%s imports %s, which its layer does not allow", name, imported)
		}
	}
}
