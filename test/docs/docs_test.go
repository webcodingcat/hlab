// Package docs_test checks that the documentation covers what the programs
// offer at the moment (requirements DOC-01 to DOC-06).
package docs_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/cli"
	"github.com/webcodingcat/hlab/internal/cloud"
	"github.com/webcodingcat/hlab/internal/settings"
)

func TestDocs(t *testing.T) {
	root := repoRoot(t)

	t.Run("DOC-01", func(t *testing.T) {
		doc := readFile(t, filepath.Join(root, "docs", "commands.md"))
		cmds := cli.Commands()
		if len(cmds) == 0 {
			t.Fatal("cli.Commands() lists no command")
		}
		for _, c := range cmds {
			if !strings.Contains(doc, "hlab "+c.Path) {
				t.Errorf("command %q is not described in docs/commands.md", "hlab "+c.Path)
			}
			for _, f := range c.Flags {
				flag := "--" + strings.TrimLeft(f, "-")
				if !containsWord(doc, flag) {
					t.Errorf("flag %s of %q is not described in docs/commands.md", flag, "hlab "+c.Path)
				}
			}
		}
		tools := cli.MCPTools()
		if len(tools) == 0 {
			t.Fatal("cli.MCPTools() lists no tool")
		}
		for _, name := range tools {
			if !containsWord(doc, name) {
				t.Errorf("MCP tool %q is not described in docs/commands.md", name)
			}
		}
	})

	t.Run("DOC-02", func(t *testing.T) {
		doc := readFile(t, filepath.Join(root, "docs", "configuration.md"))
		keys := tomlKeys(reflect.TypeFor[settings.Settings](), "")
		if len(keys) == 0 {
			t.Fatal("settings.Settings has no toml keys")
		}
		for _, k := range keys {
			name := k[strings.LastIndex(k, ".")+1:]
			if !containsWord(doc, name) {
				t.Errorf("settings.toml key %q is not described in docs/configuration.md", k)
			}
		}
		files := []string{"server-types.custom.json"}
		catalogs, _ := filepath.Glob(filepath.Join(root, "catalog", "*.json"))
		if len(catalogs) == 0 {
			t.Error("no catalog files in catalog/")
		}
		for _, c := range catalogs {
			files = append(files, filepath.Base(c))
		}
		for _, f := range files {
			if !strings.Contains(doc, f) {
				t.Errorf("file %s is not named in docs/configuration.md", f)
			}
		}
	})

	t.Run("DOC-03", func(t *testing.T) {
		coverage := readFile(t, filepath.Join(root, "docs", "development", "api-coverage.md"))
		users := readFile(t, filepath.Join(root, "docs", "deviations.md"))
		devs := api.Deviations()
		if len(devs) == 0 {
			t.Fatal("api.Deviations() lists nothing")
		}
		for _, d := range devs {
			if !strings.Contains(coverage, d.Endpoint) {
				t.Errorf("deviation %q (%s) is not listed in docs/development/api-coverage.md", d.Endpoint, d.Description)
			}
			if strings.Contains(users, d.Endpoint) {
				t.Errorf("docs/deviations.md names the endpoint %q; it describes deviations by topic without routes", d.Endpoint)
			}
		}
		routes := cloud.APIRoutes()
		if len(routes) == 0 {
			t.Fatal("cloud.APIRoutes() lists no route")
		}
		// aliases maps an internal wildcard route to the documented routes it
		// serves; any other route must appear literally on the page.
		aliases := map[string][]string{
			"GET /zones/{id}/{sub}":                    {"GET /zones/actions/{id}"},
			"GET /hetzner/v1/storage_boxes/{id}/{sub}": {"GET /hetzner/v1/storage_boxes/actions/{id}"},
		}
		for _, r := range routes {
			if strings.Contains(coverage, "`"+r+"`") {
				continue
			}
			served, ok := aliases[r]
			if !ok {
				t.Errorf("route %q that hlabd registers is not listed in docs/development/api-coverage.md", r)
				continue
			}
			for _, a := range served {
				if !strings.Contains(coverage, "`"+a+"`") {
					t.Errorf("route %q (served by %q) is not listed in docs/development/api-coverage.md", a, r)
				}
			}
		}
	})

	t.Run("DOC-04", func(t *testing.T) {
		files := []string{filepath.Join(root, "README.md")}
		_ = filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".md") {
				files = append(files, p)
			}
			return nil
		})
		for _, f := range files {
			rel, _ := filepath.Rel(root, f)
			for _, link := range markdownLinks(readFile(t, f)) {
				if msg := checkLink(root, f, link); msg != "" {
					t.Errorf("%s: link %q: %s", filepath.ToSlash(rel), link, msg)
				}
			}
		}
	})

	t.Run("DOC-05", func(t *testing.T) {
		src := filepath.Join(root, "ui", "frontend", "src")
		var views []string
		if err := json.Unmarshal([]byte(readFile(t, filepath.Join(src, "views.json"))), &views); err != nil {
			t.Fatalf("ui/frontend/src/views.json is not a JSON array of view ids: %v", err)
		}
		known := map[string]bool{}
		for _, v := range views {
			known[v] = true
			if _, err := os.Stat(filepath.Join(src, "help", v+".md")); err != nil {
				t.Errorf("view %q has no help text ui/frontend/src/help/%s.md", v, v)
			}
		}
		helps, _ := filepath.Glob(filepath.Join(src, "help", "*.md"))
		for _, h := range helps {
			if v := strings.TrimSuffix(filepath.Base(h), ".md"); !known[v] {
				t.Errorf("help text %s belongs to no view in views.json", filepath.Base(h))
			}
		}
	})

	t.Run("DOC-06", func(t *testing.T) {
		tasks := taskfileTasks(t, filepath.Join(root, "Taskfile.yml"))
		guides, _ := filepath.Glob(filepath.Join(root, "docs", "development", "*.md"))
		if len(guides) == 0 {
			t.Fatal("no developer guide in docs/development/")
		}
		for _, g := range guides {
			for _, name := range taskCommands(readFile(t, g)) {
				if !tasks[name] {
					t.Errorf("docs/development/%s: `task %s` is not a task in Taskfile.yml", filepath.Base(g), name)
				}
			}
		}
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root (go.mod) not found")
		}
		dir = parent
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// containsWord reports whether s occurs in text not as part of a longer
// identifier (so "--read-only" is not found in "--read-only-x").
func containsWord(text, s string) bool {
	isID := func(b byte) bool {
		r := rune(b)
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
	}
	for i := 0; i < len(text); {
		j := strings.Index(text[i:], s)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(s)
		if (start == 0 || !isID(text[start-1])) && (end == len(text) || !isID(text[end])) {
			return true
		}
		i = start + 1
	}
	return false
}

// tomlKeys lists the dotted keys of a settings struct from its toml tags;
// nested structs are tables.
func tomlKeys(typ reflect.Type, prefix string) []string {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	var keys []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if name == "-" {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			keys = append(keys, tomlKeys(ft, prefix)...)
			continue
		}
		if name == "" {
			name = f.Name
		}
		keys = append(keys, prefix+name)
		if ft.Kind() == reflect.Struct && ft.PkgPath() != "time" {
			keys = append(keys, tomlKeys(ft, prefix+name+".")...)
		}
	}
	return keys
}

var (
	fenceRe    = regexp.MustCompile("(?ms)^[ \t]*(?:```|~~~).*?^[ \t]*(?:```|~~~)[ \t]*\r?$")
	codeSpanRe = regexp.MustCompile("`[^`\n]*`")
	inlineRe   = regexp.MustCompile(`!?\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	refDefRe   = regexp.MustCompile(`(?m)^ {0,3}\[[^\]]+\]:[ \t]*<?([^\s>]+)>?`)
	schemeRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	headingRe  = regexp.MustCompile(`(?m)^ {0,3}#{1,6}[ \t]+(.*?)[ \t]*#*[ \t]*\r?$`)
	htmlIDRe   = regexp.MustCompile(`<a\s+(?:id|name)="([^"]+)"`)
)

// markdownLinks returns the targets of inline links, images and reference
// definitions outside code.
func markdownLinks(md string) []string {
	md = fenceRe.ReplaceAllString(md, "")
	md = codeSpanRe.ReplaceAllString(md, "")
	var links []string
	for _, m := range inlineRe.FindAllStringSubmatch(md, -1) {
		links = append(links, m[1])
	}
	for _, m := range refDefRe.FindAllStringSubmatch(md, -1) {
		links = append(links, m[1])
	}
	return links
}

// checkLink returns why a relative link does not resolve, or "".
func checkLink(root, from, link string) string {
	if schemeRe.MatchString(link) || strings.HasPrefix(link, "//") {
		return ""
	}
	path, anchor, _ := strings.Cut(link, "#")
	if p, err := url.PathUnescape(path); err == nil {
		path = p
	}
	target := from
	switch {
	case path == "":
	case strings.HasPrefix(path, "/"):
		target = filepath.Join(root, filepath.FromSlash(path))
	default:
		target = filepath.Join(filepath.Dir(from), filepath.FromSlash(path))
	}
	info, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "target does not exist"
		}
		return err.Error()
	}
	if anchor == "" {
		return ""
	}
	if info.IsDir() || !strings.EqualFold(filepath.Ext(target), ".md") {
		return "anchor on a file that is not Markdown"
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return err.Error()
	}
	if !anchors(string(b))[strings.ToLower(anchor)] {
		return "anchor not found"
	}
	return ""
}

// anchors returns the GitHub heading anchors and explicit HTML anchors.
func anchors(md string) map[string]bool {
	set := map[string]bool{}
	for _, m := range htmlIDRe.FindAllStringSubmatch(md, -1) {
		set[strings.ToLower(m[1])] = true
	}
	seen := map[string]int{}
	for _, m := range headingRe.FindAllStringSubmatch(fenceRe.ReplaceAllString(md, ""), -1) {
		slug := slugify(m[1])
		if n := seen[slug]; n > 0 {
			set[slug+"-"+strconv.Itoa(n)] = true
		} else {
			set[slug] = true
		}
		seen[slug]++
	}
	return set
}

// slugify builds a GitHub heading anchor.
func slugify(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

var (
	taskDefRe = regexp.MustCompile(`^  ([A-Za-z0-9][\w:.-]*):[ \t]*(?:#.*)?$`)
	taskUseRe = regexp.MustCompile(`(?:^|[\s;&|(])task[ \t]+([A-Za-z0-9][\w:.-]*)`)
)

// taskfileTasks lists the task names under "tasks:" in the Taskfile.
func taskfileTasks(t *testing.T, path string) map[string]bool {
	t.Helper()
	tasks := map[string]bool{}
	in := false
	for line := range strings.Lines(readFile(t, path)) {
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") {
			in = strings.HasPrefix(line, "tasks:")
			continue
		}
		if m := taskDefRe.FindStringSubmatch(line); in && m != nil {
			tasks[m[1]] = true
		}
	}
	if len(tasks) == 0 {
		t.Fatal("no tasks found in Taskfile.yml")
	}
	return tasks
}

// taskCommands returns the names in `task <name>` inside code blocks and
// code spans of a Markdown file.
func taskCommands(md string) []string {
	code := fenceRe.FindAllString(md, -1)
	code = append(code, codeSpanRe.FindAllString(fenceRe.ReplaceAllString(md, ""), -1)...)
	var names []string
	for _, c := range code {
		for _, m := range taskUseRe.FindAllStringSubmatch(strings.Trim(c, "`~"), -1) {
			names = append(names, strings.TrimRight(m[1], ".:"))
		}
	}
	return names
}
