// Package configjson holds JSON checks shared by permission config formats.
package configjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
)

// DecodeObject decodes a JSON object and rejects null anywhere within it.
// encoding/json otherwise turns null strings, slices, maps, and bools into
// their zero values, which can silently weaken policy.
func DecodeObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}

		return nil, err
	}

	if path, found := findNull(value, "$"); found {
		return nil, fmt.Errorf("%s must not be null", path)
	}

	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ must be a JSON object")
	}

	return object, nil
}

// CheckKeys rejects object keys that are not exact members of the schema.
// encoding/json matches struct fields without regard to case, so
// DisallowUnknownFields alone is not a strict schema check.
func CheckKeys(
	object map[string]any, allowed ...string,
) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}

	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if _, ok := allowedSet[key]; !ok {
			return fmt.Errorf("unknown key %q", key)
		}
	}

	return nil
}

// ObjectAt returns an object field for deeper schema checks.
func ObjectAt(
	object map[string]any, key string,
) (map[string]any, bool) {
	value, exists := object[key]
	if !exists {
		return nil, false
	}

	nested, ok := value.(map[string]any)
	return nested, ok
}

// CheckObjectValueKeys checks the keys of every object value in a map. Values
// of another type remain the typed decoder's responsibility.
func CheckObjectValueKeys(
	object map[string]any, allowed ...string,
) error {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		nested, ok := object[key].(map[string]any)
		if !ok {
			continue
		}
		if err := CheckKeys(nested, allowed...); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}

	return nil
}

func findNull(value any, path string) (string, bool) {
	if value == nil {
		return path, true
	}

	switch value := value.(type) {
	case []any:
		for i, item := range value {
			itemPath := path + "[" + strconv.Itoa(i) + "]"
			if nullPath, found := findNull(item, itemPath); found {
				return nullPath, true
			}
		}

	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}

		sort.Strings(keys)
		for _, key := range keys {
			itemPath := path + "[" + strconv.Quote(key) + "]"
			if nullPath, found := findNull(value[key], itemPath); found {
				return nullPath, true
			}
		}
	}

	return "", false
}
