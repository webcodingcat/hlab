package spectest

import (
	"strings"
	"testing"
)

// testSpec is a small spec in Hetzner's style: one operation, its answer has
// a required, nullable field, and errors have a 4xx schema.
const testSpec = `{
  "openapi": "3.1.2",
  "info": {"title": "test", "version": "1"},
  "servers": [{"url": "https://api.example.com/v1"}],
  "paths": {
    "/things/{id}": {"get": {"responses": {
      "200": {"description": "ok", "content": {"application/json": {"schema": {
        "type": "object", "required": ["thing"],
        "properties": {"thing": {"type": "object", "required": ["id", "datacenter"], "properties": {
          "id": {"type": "integer"},
          "datacenter": {"type": "string"},
          "finished": {"type": ["string", "null"]}
        }}}}}}},
      "4xx": {"description": "error", "content": {"application/json": {"schema": {
        "type": "object", "required": ["error"]}}}}
    }}},
    "/things/actions": {"get": {"responses": {"204": {"description": "none"}}}}
  }
}`

func parseTestSpec(t *testing.T, deviations ...Deviation) *Spec {
	t.Helper()
	spec, err := parse(Cloud, []byte(testSpec), deviations)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestCheckFindsWhatTheSpecDoesNotAllow(t *testing.T) {
	spec := parseTestSpec(t)
	for name, test := range map[string]struct {
		method, path string
		status       int
		body         string
		wantError    string // "" for none
	}{
		"a matching answer":    {"GET", "/v1/things/1", 200, `{"thing":{"id":1,"datacenter":"x","finished":null}}`, ""},
		"a wrong type":         {"GET", "/v1/things/1", 200, `{"thing":{"id":"one","datacenter":"x"}}`, "id"},
		"a missing field":      {"GET", "/v1/things/1", 200, `{"thing":{"id":1}}`, "datacenter"},
		"a status not listed":  {"GET", "/v1/things/1", 201, `{}`, "status 201"},
		"an error by 4xx":      {"GET", "/v1/things/1", 404, `{"error":{"code":"not_found","message":"x"}}`, ""},
		"a literal segment":    {"GET", "/v1/things/actions", 204, ``, ""},
		"a body where none is": {"GET", "/v1/things/actions", 204, `{}`, "has no body"},
		"an unknown operation": {"POST", "/v1/things/1", 201, `{}`, "no such operation"},
		"an unknown 404":       {"GET", "/v1/nothing", 404, `{"error":{"code":"not_found","message":"x","details":null}}`, ""},
		"a broken error":       {"GET", "/v1/nothing", 404, `{"message":"x"}`, "lacks"},
	} {
		err := spec.Check(test.method, test.path, test.status, "application/json", []byte(test.body))
		switch {
		case test.wantError == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)):
			t.Errorf("%s: %v, want an error about %q", name, err, test.wantError)
		}
	}
}

func TestDeviationReplacesASchemaAndItsRequired(t *testing.T) {
	spec := parseTestSpec(t, Deviation{
		Spec:    Cloud,
		Pointer: "/paths/*/get/responses/200/content/application~1json/schema/properties/thing/properties/datacenter",
		Reason:  "Hetzner removed datacenters",
	})
	if err := spec.Check("GET", "/v1/things/1", 200, "application/json", []byte(`{"thing":{"id":1}}`)); err != nil {
		t.Errorf("the deviation does not hold: %v", err)
	}
	if err := spec.Check("GET", "/v1/things/1", 200, "application/json", []byte(`{"thing":{"id":"one"}}`)); err == nil {
		t.Error("the deviation turned off the rest of the schema")
	}
}

func TestDeviationWithAValue(t *testing.T) {
	spec := parseTestSpec(t, Deviation{
		Spec:    Cloud,
		Pointer: "/paths/~1things~1{id}/get/responses/200/content/application~1json/schema/properties/thing/properties/id",
		Value:   map[string]any{"type": "string"},
		Reason:  "the real API sends IDs as strings here",
	})
	if err := spec.Check("GET", "/v1/things/1", 200, "application/json", []byte(`{"thing":{"id":"one","datacenter":"x"}}`)); err != nil {
		t.Errorf("the value does not hold: %v", err)
	}
}

func TestPlainTextNeedsItsMediaTypeInTheSpec(t *testing.T) {
	plain := "text/plain; charset=utf-8"
	if err := parseTestSpec(t).Check("GET", "/v1/things/1", 404, plain, []byte("Not Found\n")); err == nil {
		t.Error("a plain-text answer passes, though the spec lists only JSON")
	}
	spec := parseTestSpec(t, Deviation{
		Spec:    Cloud,
		Pointer: "/paths/~1things~1{id}/get/responses/4xx/content/text~1plain",
		Reason:  "the real API answers plain text",
	})
	if err := spec.Check("GET", "/v1/things/1", 404, plain, []byte("Not Found\n")); err != nil {
		t.Errorf("the added media type does not hold: %v", err)
	}
}

func TestDeviationThatMatchesNothingIsAnError(t *testing.T) {
	_, err := parse(Cloud, []byte(testSpec), []Deviation{{Spec: Cloud, Pointer: "/paths/~1gone/get", Reason: "x"}})
	if err == nil || !strings.Contains(err.Error(), "matches nothing") {
		t.Errorf("parsing: %v, want an error about a deviation that matches nothing", err)
	}
}

func TestDeviationOfTheOtherSpecIsLeftOut(t *testing.T) {
	if _, err := parse(Cloud, []byte(testSpec), []Deviation{{Spec: Hetzner, Pointer: "/paths/~1gone", Reason: "x"}}); err != nil {
		t.Errorf("a deviation of the Hetzner spec applied to the Cloud spec: %v", err)
	}
}

func TestTheSpecsOfTheRepositoryLoad(t *testing.T) {
	for _, name := range []string{Cloud, Hetzner} {
		spec, err := Load(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if spec.base != "/v1" || len(spec.operations) == 0 {
			t.Errorf("%s: base %q with %d operations", name, spec.base, len(spec.operations))
		}
	}
}
