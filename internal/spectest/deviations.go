package spectest

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Deviation is an entry of compat/spec-deviations.yaml: where the real API
// differs from the spec, and the schema that holds instead.
type Deviation struct {
	Spec    string `yaml:"spec"`
	Pointer string `yaml:"pointer"`
	Value   any    `yaml:"value"`
	Reason  string `yaml:"reason"`
}

// readDeviations reads compat/spec-deviations.yaml; every entry needs a
// spec, a pointer and a reason.
func readDeviations(path string) ([]Deviation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Deviations []Deviation `yaml:"deviations"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, deviation := range file.Deviations {
		if deviation.Spec != Cloud && deviation.Spec != Hetzner {
			return nil, fmt.Errorf("%s: the deviation %s names the spec %q", path, deviation.Pointer, deviation.Spec)
		}
		if !strings.HasPrefix(deviation.Pointer, "/") || deviation.Reason == "" {
			return nil, fmt.Errorf("%s: the deviation %q needs a pointer starting with / and a reason", path, deviation.Pointer)
		}
	}
	return file.Deviations, nil
}

// apply replaces what the deviation's pointer matches in the raw spec with
// its value ({} when it has none); a last token that names a key an object
// lacks adds it (a media type the spec does not list). A replaced property
// also leaves its object's "required". A pointer that matches nothing is an
// error: the spec has changed under the deviation.
func (d Deviation) apply(spec map[string]any) error {
	value := d.Value
	if value == nil {
		value = map[string]any{}
	}
	var tokens []string
	for token := range strings.SplitSeq(strings.TrimPrefix(d.Pointer, "/"), "/") {
		// RFC 6901: ~1 is "/", ~0 is "~", in that order
		tokens = append(tokens, strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~"))
	}
	if len(replace(spec, tokens, value)) == 0 {
		return fmt.Errorf("the deviation %s matches nothing in the spec", d.Pointer)
	}
	return nil
}

// replace walks node along tokens ("*" matches every key or index) and
// replaces or adds what the last token names with value. It returns the keys
// it replaced, so that the schema holding "properties" can drop them from its
// "required".
func replace(node any, tokens []string, value any) []string {
	token, last := tokens[0], len(tokens) == 1
	var replaced []string
	switch container := node.(type) {
	case map[string]any:
		if _, found := container[token]; last && token != "*" && !found {
			container[token] = value
			return []string{token}
		}
		for key, child := range container {
			if token != "*" && token != key {
				continue
			}
			if last {
				container[key] = value
				replaced = append(replaced, key)
				continue
			}
			names := replace(child, tokens[1:], value)
			replaced = append(replaced, names...)
			if key == "properties" && len(tokens) == 2 {
				dropRequired(container, names)
			}
		}
	case []any:
		for i, child := range container {
			if token != "*" && token != strconv.Itoa(i) {
				continue
			}
			if last {
				container[i] = value
				replaced = append(replaced, strconv.Itoa(i))
				continue
			}
			replaced = append(replaced, replace(child, tokens[1:], value)...)
		}
	}
	return replaced
}

// dropRequired takes names out of a schema's "required".
func dropRequired(schema map[string]any, names []string) {
	required, ok := schema["required"].([]any)
	if !ok {
		return
	}
	schema["required"] = slices.DeleteFunc(required, func(name any) bool {
		text, ok := name.(string)
		return ok && slices.Contains(names, text)
	})
}
