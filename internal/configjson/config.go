// Package configjson decodes the JSON files that shape permission policy.
package configjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

var unmarshalerType = reflect.TypeFor[json.Unmarshaler]()

type schemaPolicy uint8

const (
	openSchema schemaPolicy = iota
	closedSchema
)

// Decode checks the complete JSON document before decoding it into dst. Object
// keys must be unique at every depth. A struct destination also makes the
// document a closed schema whose exact keys come from its JSON tags and whose
// values may not be null. A map destination remains open so callers can retain
// fields owned by another application.
func Decode(data []byte, dst any) error {
	destination, policy, err := destinationType(dst)
	if err != nil {
		return err
	}
	if policy == closedSchema {
		if err := checkSchema(destination, make(map[reflect.Type]bool)); err != nil {
			return err
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanValue(decoder, "$", destination, policy); err != nil {
		return err
	}

	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}

		return err
	}

	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(dst)
}

func destinationType(dst any) (reflect.Type, schemaPolicy, error) {
	value := reflect.ValueOf(dst)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return nil, openSchema, fmt.Errorf(
			"JSON destination must be a non-nil pointer")
	}

	typeOf := indirectType(value.Type().Elem())
	switch typeOf.Kind() {
	case reflect.Struct:
		return typeOf, closedSchema, nil
	case reflect.Map:
		return typeOf, openSchema, nil
	default:
		return nil, openSchema, fmt.Errorf(
			"JSON destination must point to a struct or map, not %s", typeOf,
		)
	}
}

func scanValue(
	decoder *json.Decoder, path string, expected reflect.Type,
	policy schemaPolicy,
) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if policy == closedSchema {
			return fmt.Errorf("%s must not be null", path)
		}

		return nil
	}

	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delimiter {
	case '{':
		return scanObject(decoder, path, expected, policy)
	case '[':
		return scanArray(decoder, path, expected, policy)
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delimiter, path)
	}
}

func scanObject(
	decoder *json.Decoder, path string, expected reflect.Type,
	policy schemaPolicy,
) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}

		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("object key at %s is not a string", path)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate key %q at %s", key, path)
		}

		seen[key] = struct{}{}

		childType, err := objectValueType(expected, key, path, policy)
		if err != nil {
			return err
		}

		childPath := path + "[" + strconv.Quote(key) + "]"
		if err := scanValue(decoder, childPath, childType, policy); err != nil {
			return err
		}
	}

	return consumeDelimiter(decoder, '}', path)
}

func scanArray(
	decoder *json.Decoder, path string, expected reflect.Type,
	policy schemaPolicy,
) error {
	elementType := arrayElementType(expected)
	for index := 0; decoder.More(); index++ {
		childPath := path + "[" + strconv.Itoa(index) + "]"
		if err := scanValue(decoder, childPath, elementType, policy); err != nil {
			return err
		}
	}

	return consumeDelimiter(decoder, ']', path)
}

func consumeDelimiter(
	decoder *json.Decoder, expected json.Delim, path string,
) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != expected {
		return fmt.Errorf("expected JSON delimiter %q at %s", expected, path)
	}

	return nil
}

func objectValueType(
	expected reflect.Type, key, path string, policy schemaPolicy,
) (reflect.Type, error) {
	if policy == openSchema || expected == nil {
		return nil, nil
	}

	expected = indirectType(expected)
	switch expected.Kind() {
	case reflect.Struct:
		fields, err := structFields(expected)
		if err != nil {
			return nil, err
		}

		fieldType, exists := fields[key]
		if !exists {
			return nil, fmt.Errorf("unknown key %q at %s", key, path)
		}

		return fieldType, nil
	case reflect.Map:
		return expected.Elem(), nil
	default:
		// The typed decoder will report the shape mismatch. There is no
		// valid object schema to enforce for a value of this type.
		return nil, nil
	}
}

func arrayElementType(expected reflect.Type) reflect.Type {
	if expected == nil {
		return nil
	}

	expected = indirectType(expected)
	switch expected.Kind() {
	case reflect.Array, reflect.Slice:
		return expected.Elem()
	default:
		return nil
	}
}

func checkSchema(typeOf reflect.Type, visiting map[reflect.Type]bool) error {
	typeOf = indirectType(typeOf)
	if implementsUnmarshaler(typeOf) {
		return fmt.Errorf(
			"unsupported JSON schema type %s: custom unmarshaling", typeOf,
		)
	}
	if visiting[typeOf] {
		return fmt.Errorf("unsupported recursive JSON schema type %s", typeOf)
	}

	switch typeOf.Kind() {
	case reflect.Struct:
		visiting[typeOf] = true
		defer delete(visiting, typeOf)

		fields, err := structFields(typeOf)
		if err != nil {
			return err
		}

		for name, fieldType := range fields {
			if err := checkSchema(fieldType, visiting); err != nil {
				return fmt.Errorf("JSON field %q: %w", name, err)
			}
		}

		return nil

	case reflect.Map:
		if typeOf.Key().Kind() != reflect.String {
			return fmt.Errorf(
				"unsupported JSON schema map key type %s", typeOf.Key(),
			)
		}

		visiting[typeOf] = true
		defer delete(visiting, typeOf)
		return checkSchema(typeOf.Elem(), visiting)

	case reflect.Array, reflect.Slice:
		visiting[typeOf] = true
		defer delete(visiting, typeOf)
		return checkSchema(typeOf.Elem(), visiting)

	case reflect.Interface:
		return fmt.Errorf("unsupported open value in closed JSON schema: %s", typeOf)

	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return nil

	default:
		return fmt.Errorf("unsupported JSON schema type %s", typeOf)
	}
}

func structFields(typeOf reflect.Type) (map[string]reflect.Type, error) {
	fields := make(map[string]reflect.Type)
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		if !field.IsExported() {
			continue
		}

		tag, exists := field.Tag.Lookup("json")
		if !exists {
			return nil, fmt.Errorf(
				"unsupported JSON schema field %s.%s: explicit json tag required",
				typeOf, field.Name,
			)
		}
		if tag == "-" {
			continue
		}
		if field.Anonymous {
			return nil, fmt.Errorf(
				"unsupported anonymous JSON schema field %s.%s",
				typeOf, field.Name,
			)
		}

		name, _, _ := strings.Cut(tag, ",")
		if !supportedTagName(name) {
			return nil, fmt.Errorf(
				"unsupported json tag %q on %s.%s",
				tag, typeOf, field.Name,
			)
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, fmt.Errorf(
				"duplicate json tag %q on %s", name, typeOf,
			)
		}

		fields[name] = field.Type
	}

	return fields, nil
}

func supportedTagName(name string) bool {
	if name == "" {
		return false
	}

	for _, character := range name {
		if unicode.IsLetter(character) || unicode.IsDigit(character) ||
			character == '_' || character == '-' {
			continue
		}

		return false
	}

	return true
}

func implementsUnmarshaler(typeOf reflect.Type) bool {
	if typeOf.Implements(unmarshalerType) {
		return true
	}

	return typeOf.Kind() != reflect.Pointer &&
		reflect.PointerTo(typeOf).Implements(unmarshalerType)
}

func indirectType(typeOf reflect.Type) reflect.Type {
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}

	return typeOf
}
