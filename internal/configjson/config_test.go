package configjson

import (
	"encoding/json"
	"strings"
	"testing"
)

type testConfig struct {
	Deny  testTier                  `json:"Deny,omitempty"`
	Rules map[string]testRuleConfig `json:"Rules,omitempty"`
	Items []map[string]string       `json:"Items,omitempty"`
}

type testTier struct {
	Commands map[string]string `json:"Commands,omitempty"`
}

type testRuleConfig struct {
	Enabled bool `json:"Enabled"`
}

func TestDecodeClosedSchema(t *testing.T) {
	t.Parallel()

	var config testConfig
	err := Decode([]byte(`{
		"Deny": {"Commands": {"ssh:*": "remote access"}},
		"Rules": {"shell": {"Enabled": true}}
	}`), &config)
	if err != nil {
		t.Fatalf("Decode returned an error: %v", err)
	}
	if got := config.Deny.Commands["ssh:*"]; got != "remote access" {
		t.Fatalf("Deny command reason = %q, want %q", got, "remote access")
	}
	if !config.Rules["shell"].Enabled {
		t.Fatal("Rules[shell].Enabled = false, want true")
	}
}

func TestDecodeRejectsDuplicateKeysAtEveryDepth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		key  string
	}{
		{
			name: "root",
			json: `{"Deny": {}, "Deny": {}}`,
			key:  "Deny",
		},
		{
			name: "nested map",
			json: `{"Deny":{"Commands":{"ssh:*":"one","ssh:*":"two"}}}`,
			key:  "ssh:*",
		},
		{
			name: "inside array",
			json: `{"Items":[{"value":"one","value":"two"}]}`,
			key:  "value",
		},
		{
			name: "escaped equivalent",
			json: `{"Deny":{},"\u0044eny":{}}`,
			key:  "Deny",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var config testConfig
			err := Decode([]byte(test.json), &config)
			if err == nil {
				t.Fatal("Decode succeeded, want duplicate-key error")
			}
			if !strings.Contains(err.Error(), "duplicate key \""+test.key+"\"") {
				t.Fatalf("Decode error = %q, want duplicate key %q", err, test.key)
			}
		})
	}
}

func TestDecodeRejectsTrailingValue(t *testing.T) {
	t.Parallel()

	var value map[string]any
	err := Decode([]byte(`{} {}`), &value)
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("Decode error = %v, want multiple JSON values", err)
	}
}

func TestDecodeStructUsesExactJSONTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		key  string
	}{
		{name: "unknown", json: `{"Extra": true}`, key: "Extra"},
		{name: "wrong case", json: `{"deny": {}}`, key: "deny"},
		{
			name: "nested struct",
			json: `{"Rules":{"shell":{"enabled":true}}}`,
			key:  "enabled",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var config testConfig
			err := Decode([]byte(test.json), &config)
			if err == nil {
				t.Fatal("Decode succeeded, want unknown-key error")
			}
			if !strings.Contains(err.Error(), "unknown key \""+test.key+"\"") {
				t.Fatalf("Decode error = %q, want unknown key %q", err, test.key)
			}
		})
	}
}

func TestDecodeStructRejectsNullAtEveryDepth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		path string
	}{
		{name: "root", json: `null`, path: "$"},
		{name: "field", json: `{"Deny":null}`, path: `$["Deny"]`},
		{
			name: "map value",
			json: `{"Deny":{"Commands":{"ssh:*":null}}}`,
			path: `$["Deny"]["Commands"]["ssh:*"]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var config testConfig
			err := Decode([]byte(test.json), &config)
			if err == nil {
				t.Fatal("Decode succeeded, want null error")
			}
			if !strings.Contains(err.Error(), test.path+" must not be null") {
				t.Fatalf("Decode error = %q, want path %s", err, test.path)
			}
		})
	}
}

func TestDecodeMapAllowsOpenFieldsAndNull(t *testing.T) {
	t.Parallel()

	var value map[string]any
	err := Decode([]byte(`{
		"future": {"nested": null},
		"large": 9007199254740993
	}`), &value)
	if err != nil {
		t.Fatalf("Decode returned an error: %v", err)
	}
	if got := value["large"]; got != json.Number("9007199254740993") {
		t.Fatalf("large = %#v, want json.Number", got)
	}

	nested, ok := value["future"].(map[string]any)
	if !ok || nested["nested"] != nil {
		t.Fatalf("future = %#v, want nested null", value["future"])
	}
}

func TestDecodeMapRejectsNestedDuplicate(t *testing.T) {
	t.Parallel()

	var value map[string]any
	err := Decode(
		[]byte(`{"future":[{"value":1,"value":2}]}`), &value,
	)
	if err == nil || !strings.Contains(err.Error(), `duplicate key "value"`) {
		t.Fatalf("Decode error = %v, want nested duplicate-key error", err)
	}
}

func TestDecodeRejectsUnsupportedClosedSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dst  any
	}{
		{
			name: "missing tag",
			dst:  &struct{ Value string }{},
		},
		{
			name: "open interface",
			dst: &struct {
				Value any `json:"Value"`
			}{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := Decode([]byte(`{}`), test.dst); err == nil {
				t.Fatal("Decode succeeded, want unsupported-schema error")
			}
		})
	}
}
