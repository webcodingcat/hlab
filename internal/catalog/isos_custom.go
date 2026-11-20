package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/webcodingcat/hlab/internal/images"
)

// OwnISOsFile is the file of the own ISOs in the config folder.
const OwnISOsFile = "isos.custom.json"

// MinOwnISOID is the smallest ID of an own ISO.
const MinOwnISOID int64 = 100000

// ISOSource is `_hlab.source` of an own ISO: exactly one of URL and Path.
type ISOSource struct {
	URL    string `json:"url,omitempty"`
	Path   string `json:"path,omitempty"`   // local .iso file
	SHA256 string `json:"sha256,omitempty"` // optional, 64 hex
}

// OwnISOSpec is an own ISO to add (ID 0) or update (ID set).
type OwnISOSpec struct {
	ID           int64     `json:"id"`
	Description  string    `json:"description"`  // required; the shown name
	Architecture string    `json:"architecture"` // "x86" or "arm"
	Source       ISOSource `json:"source"`
}

// ownISOEntry is an entry of the own ISOs file.
type ownISOEntry struct {
	ID           int64  `json:"id"`
	Description  string `json:"description"`
	Architecture string `json:"architecture"`
	Hlab         struct {
		Name   string    `json:"name"`
		Source ISOSource `json:"source"`
	} `json:"_hlab"`
}

// OwnISOCacheName is the cache file name of an own ISO:
// own-<id>-<first 8 hex of SHA-256 of the source JSON>.iso.
func OwnISOCacheName(id int64, src ISOSource) string {
	b, _ := json.Marshal(src) // a struct of strings always marshals
	sum := sha256.Sum256(b)
	return fmt.Sprintf("own-%d-%s.iso", id, hex.EncodeToString(sum[:])[:8])
}

// iso returns the ISO served for e: the Hetzner iso object without `_hlab`.
func (e ownISOEntry) iso() ISO {
	var arch any
	if e.Architecture != "" {
		arch = e.Architecture
	}
	raw, _ := json.Marshal(map[string]any{
		"id":           e.ID,
		"name":         nil,
		"description":  e.Description,
		"type":         "private",
		"deprecation":  nil,
		"architecture": arch,
	})
	return ISO{
		ID:           e.ID,
		Architecture: e.Architecture,
		Source:       images.Source{URL: e.Hlab.Source.URL, Path: e.Hlab.Source.Path, SHA256: e.Hlab.Source.SHA256, Format: "iso"},
		Raw:          raw,
		Own:          true,
		CacheName:    e.Hlab.Name,
		OwnSource:    e.Hlab.Source,
	}
}

// label names an own ISO in a warning.
func (e ownISOEntry) label() string {
	return fmt.Sprintf("own ISO %d (%q)", e.ID, e.Description)
}

// readOwnISOsRaw reads the objects of file and their decoded entries; a
// missing file has none.
func readOwnISOsRaw(file string) ([]json.RawMessage, []ownISOEntry, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	entries := make([]ownISOEntry, len(raw))
	for i, r := range raw {
		if err := json.Unmarshal(r, &entries[i]); err != nil {
			return nil, nil, err
		}
	}
	return raw, entries, nil
}

// checkOwnISOs reports the first entry with an ID below MinOwnISOID, an ID
// of a catalog ISO, a duplicate ID or cache name, or no cache name.
func (c *Catalog) checkOwnISOs(entries []ownISOEntry) error {
	ids := map[int64]bool{}
	for _, iso := range c.ISOs {
		ids[iso.ID] = true
	}
	names := map[string]bool{}
	for _, e := range entries {
		switch {
		case e.ID < MinOwnISOID:
			return fmt.Errorf("%s: own IDs start at %d", e.label(), MinOwnISOID)
		case ids[e.ID]:
			return fmt.Errorf("%s: the ID is taken", e.label())
		case e.Hlab.Name == "":
			return fmt.Errorf("%s: no _hlab.name", e.label())
		case names[e.Hlab.Name]:
			return fmt.Errorf("%s: the cache name %q is taken", e.label(), e.Hlab.Name)
		}
		ids[e.ID] = true
		names[e.Hlab.Name] = true
	}
	return nil
}

// ReloadOwnISOs reads file (missing: none) and replaces the served own
// ISOs. An invalid file, an ID below MinOwnISOID or a duplicate ID or cache
// name serves none; a Path source whose file cannot be opened leaves out
// only that ISO. Each returned warning is one line for the custom_isos
// problem.
func (c *Catalog) ReloadOwnISOs(file string) []string {
	var own []ISO
	var warns []string
	_, entries, err := readOwnISOsRaw(file)
	if err == nil {
		err = c.checkOwnISOs(entries)
	}
	if err != nil {
		warns = append(warns, fmt.Sprintf("own ISOs in %s are ignored: %v", file, err))
		entries = nil
	}
	for _, e := range entries {
		if p := e.Hlab.Source.Path; p != "" {
			f, err := os.Open(p)
			if err != nil {
				warns = append(warns, fmt.Sprintf("%s is left out: %v", e.label(), err))
				continue
			}
			_ = f.Close()
		}
		own = append(own, e.iso())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ownISOs = own
	c.ownISOWarnings = warns
	return slices.Clone(warns)
}

// DropOwnISO stops serving the own ISO id, for example after a failed
// copy, and adds a warning naming it with reason.
func (c *Catalog) DropOwnISO(id int64, reason string) {
	c.dropOwnISO(func(iso ISO) bool { return iso.ID == id }, reason)
}

// DropOwnISOByCacheName stops serving the own ISO with cache name name,
// if one is served, and adds a warning naming it with reason. A copy that
// fails after a reload changed the ISO's source drops nothing.
func (c *Catalog) DropOwnISOByCacheName(name, reason string) {
	c.dropOwnISO(func(iso ISO) bool { return iso.CacheName == name }, reason)
}

// dropOwnISO stops serving the first own ISO match accepts.
func (c *Catalog) dropOwnISO(match func(ISO) bool, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := slices.IndexFunc(c.ownISOs, match)
	if i < 0 {
		return
	}
	var desc struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(c.ownISOs[i].Raw, &desc) // Raw is built by iso()
	c.ownISOWarnings = append(c.ownISOWarnings, fmt.Sprintf("own ISO %d (%q) is left out: %s", c.ownISOs[i].ID, desc.Description, reason))
	c.ownISOs = slices.Delete(slices.Clone(c.ownISOs), i, i+1)
}

// OwnISOsWarnings returns why own ISOs are left out; none while all are
// served.
func (c *Catalog) OwnISOsWarnings() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.ownISOWarnings)
}

// OwnISOs returns the own ISOs served now.
func (c *Catalog) OwnISOs() []ISO {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.ownISOs)
}

// AllISOs returns the catalog ISOs followed by the own ISOs.
func (c *Catalog) AllISOs() []ISO {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append(slices.Clone(c.ISOs), c.ownISOs...)
}

// ISOSourceByCacheName returns the source of the served own ISO with cache
// name name, for images.Options.ISOSource.
func (c *Catalog) ISOSourceByCacheName(name string) (images.Source, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, iso := range c.ownISOs {
		if iso.CacheName == name {
			return iso.Source, true
		}
	}
	return images.Source{}, false
}

var sha256Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// checkOwnISOSpec checks the fields of s other than its ID.
func checkOwnISOSpec(s OwnISOSpec) error {
	src := s.Source
	switch {
	case strings.TrimSpace(s.Description) == "":
		return &FieldError{"description", "is required"}
	case s.Architecture != "x86" && s.Architecture != "arm":
		return &FieldError{"architecture", `must be "x86" or "arm"`}
	case src.URL == "" && src.Path == "":
		return &FieldError{"url", "a URL or a local file is required"}
	case src.URL != "" && src.Path != "":
		return &FieldError{"url", "give either a URL or a local file, not both"}
	case src.SHA256 != "" && !sha256Re.MatchString(src.SHA256):
		return &FieldError{"sha256", "must be 64 hexadecimal characters"}
	case src.Path != "" && !strings.EqualFold(fileExt(src.Path), ".iso"):
		return &FieldError{"path", "must be an .iso file"}
	}
	if src.URL != "" {
		u, err := url.Parse(src.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return &FieldError{"url", "must be an http or https URL"}
		}
	}
	return nil
}

// fileExt returns the extension of a Windows or slash path.
func fileExt(p string) string {
	i := strings.LastIndexAny(p, `.\/`)
	if i < 0 || p[i] != '.' {
		return ""
	}
	return p[i:]
}

// ownISOObject builds the file entry of s with ID id and cache name name.
func ownISOObject(s OwnISOSpec, id int64, name string) (json.RawMessage, ISO, error) {
	var e ownISOEntry
	e.ID, e.Description, e.Architecture = id, s.Description, s.Architecture
	e.Hlab.Name, e.Hlab.Source = name, s.Source
	obj, err := json.Marshal(map[string]any{
		"id":           id,
		"name":         nil,
		"description":  s.Description,
		"type":         "private",
		"deprecation":  nil,
		"architecture": s.Architecture,
		"_hlab":        e.Hlab,
	})
	if err != nil {
		return nil, ISO{}, err
	}
	return obj, e.iso(), nil
}

// AddOwnISO checks s, gives it the next free ID from MinOwnISOID (not used
// by a catalog ISO, a served own ISO or an entry of file) and its cache
// name, and rewrites file in place in one write.
func AddOwnISO(c *Catalog, file string, s OwnISOSpec) (ISO, error) {
	raw, entries, err := readOwnISOsRaw(file)
	if err != nil {
		return ISO{}, fmt.Errorf("read %s: %w", file, err)
	}
	if err := checkOwnISOSpec(s); err != nil {
		return ISO{}, err
	}
	taken := map[int64]bool{}
	for _, iso := range c.AllISOs() {
		taken[iso.ID] = true
	}
	for _, e := range entries {
		taken[e.ID] = true
	}
	id := int64(MinOwnISOID)
	for taken[id] {
		id++
	}
	obj, iso, err := ownISOObject(s, id, OwnISOCacheName(id, s.Source))
	if err != nil {
		return ISO{}, err
	}
	if err := writeOwn(file, append(raw, obj)); err != nil {
		return ISO{}, err
	}
	return iso, nil
}

// UpdateOwnISO replaces the entry with ID s.ID in file with the values of
// s, checked as by AddOwnISO; it keeps the ID and the position and changes
// the cache name only when the source changed. The error wraps
// fs.ErrNotExist when file has no entry with that ID.
func UpdateOwnISO(c *Catalog, file string, s OwnISOSpec) (ISO, error) {
	raw, entries, err := readOwnISOsRaw(file)
	if err != nil {
		return ISO{}, fmt.Errorf("read %s: %w", file, err)
	}
	i := slices.IndexFunc(entries, func(e ownISOEntry) bool { return e.ID == s.ID })
	if i < 0 {
		return ISO{}, fmt.Errorf("own ISO %d: %w", s.ID, fs.ErrNotExist)
	}
	if err := checkOwnISOSpec(s); err != nil {
		return ISO{}, err
	}
	name := entries[i].Hlab.Name
	if s.Source != entries[i].Hlab.Source || name == "" {
		name = OwnISOCacheName(s.ID, s.Source)
	}
	obj, iso, err := ownISOObject(s, s.ID, name)
	if err != nil {
		return ISO{}, err
	}
	raw[i] = obj
	if err := writeOwn(file, raw); err != nil {
		return ISO{}, err
	}
	return iso, nil
}

// RemoveOwnISO removes the entry with ID id from file in place; the error
// wraps fs.ErrNotExist when the file has no such entry.
func RemoveOwnISO(file string, id int64) error {
	raw, entries, err := readOwnISOsRaw(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	i := slices.IndexFunc(entries, func(e ownISOEntry) bool { return e.ID == id })
	if i < 0 {
		return fmt.Errorf("own ISO %d: %w", id, fs.ErrNotExist)
	}
	return writeOwn(file, slices.Delete(raw, i, i+1))
}
