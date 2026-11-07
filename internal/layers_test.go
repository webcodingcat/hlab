package internal_test

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const module = "github.com/webcodingcat/hlab/"

// layerHeader starts the layer table in DEVELOPING.md, Layers.
const layerHeader = "| Package | Job | May import |"

// quoted finds the names in backticks in a cell of the table.
var quoted = regexp.MustCompile("`([^`]+)`")

// allowedImports reads the layer table of DEVELOPING.md, the one place it is
// kept: for each package below internal/, the packages of this module it may
// import. Rows that may import "anything" (the commands) are left out, and so
// is every package of cmd/.
func allowedImports(t *testing.T) map[string][]string {
	t.Helper()
	text, err := os.ReadFile("../DEVELOPING.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, found := strings.Cut(string(text), layerHeader+"\n")
	if !found {
		t.Fatalf("DEVELOPING.md has no line %q", layerHeader)
	}
	allowed := map[string][]string{}
	for _, row := range strings.Split(table, "\n")[1:] { // [0] is the |---| line
		cells := strings.Split(row, "|")
		if len(cells) != 5 {
			break // the table ends
		}
		if strings.TrimSpace(cells[3]) == "anything" {
			continue
		}
		var imports []string
		for _, match := range quoted.FindAllStringSubmatch(cells[3], -1) {
			imports = append(imports, "internal/"+match[1])
		}
		pkg := quoted.FindStringSubmatch(cells[1])
		if pkg == nil {
			t.Fatalf("a row of the layer table names no package: %s", row)
		}
		allowed[pkg[1]] = imports
	}
	if len(allowed) == 0 {
		t.Fatal("the layer table in DEVELOPING.md is empty")
	}
	return allowed
}

func TestLayerTableHasEveryPackage(t *testing.T) {
	// a check of the parser: a change of the table's format must not leave
	// the test with only part of the rows
	table := allowedImports(t)
	for _, pkg := range []string{"internal/tray", "internal/state", "internal/spectest"} {
		if _, found := table[pkg]; !found {
			t.Errorf("the layer table read from DEVELOPING.md lacks %s", pkg)
		}
	}
}

func TestEveryAllowedImportIsAPackageOfTheTable(t *testing.T) {
	table := allowedImports(t)
	for pkg, allowed := range table {
		for _, imported := range allowed {
			if _, known := table[imported]; !known {
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
	table := allowedImports(t)
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
				checkImports(t, table, pkg)
			}
		})
	}
}

// checkImports reports every import of a package of this module that the
// table does not allow, and a package under internal/ that is not in it.
func checkImports(t *testing.T, table map[string][]string, pkg *packages.Package) {
	t.Helper()
	name := strings.TrimPrefix(pkg.PkgPath, module)
	if strings.HasPrefix(name, "cmd/") || len(pkg.GoFiles) == 0 {
		return // commands wire everything; test-only packages import nothing
	}
	allowed, known := table[name]
	if !known {
		t.Errorf("%s is not in the layer table of DEVELOPING.md", name)
		return
	}
	for path := range pkg.Imports {
		imported, ours := strings.CutPrefix(path, module)
		if ours && !slices.Contains(allowed, imported) {
			t.Errorf("%s imports %s, which its layer does not allow", name, imported)
		}
	}
}
