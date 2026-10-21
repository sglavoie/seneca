package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// A local validator for the subset of JSON Schema `reviewSchema` is written in:
// object, array, string and null types, `properties`, `required`, `items`, and
// `additionalProperties`. Seneca compiles the very constant it hands the
// harness, so the reply is checked against the schema that was actually
// requested rather than against a second description of it that could drift.
//
// This is deliberately not a general JSON Schema implementation: compiling
// refuses any keyword it does not enforce, so adding a constraint to
// `reviewSchema` fails loudly here instead of being silently ignored.
type jsonSchema struct {
	types                []string
	properties           map[string]*jsonSchema
	required             []string
	additionalProperties bool
	items                *jsonSchema
}

// schemaNode is the on-disk shape of a compiled node. Decoding it with
// DisallowUnknownFields is what turns an unsupported keyword into a compile
// error.
type schemaNode struct {
	Type                 json.RawMessage        `json:"type"`
	Properties           map[string]*schemaNode `json:"properties"`
	Required             []string               `json:"required"`
	AdditionalProperties *bool                  `json:"additionalProperties"`
	Items                *schemaNode            `json:"items"`
}

// compiledReviewSchema is built from reviewSchema at startup. A failure here can
// only come from editing the constant, which is why it panics like a compiled
// regexp rather than surfacing at review time.
var compiledReviewSchema = mustCompileJSONSchema(reviewSchema)

func mustCompileJSONSchema(raw string) *jsonSchema {
	schema, err := compileJSONSchema(raw)
	if err != nil {
		panic(fmt.Sprintf("compiling review schema: %v", err))
	}
	return schema
}

func compileJSONSchema(raw string) (*jsonSchema, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()

	var root schemaNode
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("trailing content after the schema")
	}
	return compileSchemaNode(&root, "")
}

func compileSchemaNode(node *schemaNode, path string) (*jsonSchema, error) {
	label := schemaLabel(path)

	types, err := compileSchemaTypes(node.Type)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}

	schema := &jsonSchema{types: types, additionalProperties: true}
	if node.AdditionalProperties != nil {
		schema.additionalProperties = *node.AdditionalProperties
	}

	if len(node.Properties) > 0 {
		schema.properties = make(map[string]*jsonSchema, len(node.Properties))
		for name, child := range node.Properties {
			compiled, err := compileSchemaNode(child, childSchemaPath(path, name))
			if err != nil {
				return nil, err
			}
			schema.properties[name] = compiled
		}
	}

	for _, name := range node.Required {
		if _, ok := schema.properties[name]; !ok {
			return nil, fmt.Errorf("%s: required property %q has no definition", label, name)
		}
	}
	schema.required = append(schema.required, node.Required...)

	if node.Items != nil {
		compiled, err := compileSchemaNode(node.Items, path+"[]")
		if err != nil {
			return nil, err
		}
		schema.items = compiled
	}
	return schema, nil
}

// compileSchemaTypes accepts both spellings JSON Schema allows: a single type
// name, or a list of them (which is how `focus_assessment` says "object or
// null").
func compileSchemaTypes(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("declares no type")
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return validatedSchemaTypes([]string{single})
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("declares an unreadable type")
	}
	if len(many) == 0 {
		return nil, fmt.Errorf("declares an empty type list")
	}
	return validatedSchemaTypes(many)
}

func validatedSchemaTypes(types []string) ([]string, error) {
	for _, name := range types {
		switch name {
		case jsonKindObject, jsonKindArray, jsonKindString, jsonKindNumber, jsonKindBoolean, jsonKindNull:
		default:
			return nil, fmt.Errorf("declares unsupported type %q", name)
		}
	}
	return types, nil
}

const (
	jsonKindObject  = "object"
	jsonKindArray   = "array"
	jsonKindString  = "string"
	jsonKindNumber  = "number"
	jsonKindBoolean = "boolean"
	jsonKindNull    = "null"
)

// decodeStrictJSON reads exactly one JSON value and refuses anything after it,
// so a reply carrying a second object cannot have its tail quietly dropped.
// Numbers stay json.Number so validation reports what was sent rather than a
// float rendering of it.
func decodeStrictJSON(payload []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("invalid JSON: unexpected content after the response")
	}
	return value, nil
}

// validate reports the first way value departs from the schema. path is the
// dotted, indexed location inside the response ("" at the root).
func (s *jsonSchema) validate(value any, path string) error {
	kind := jsonKindOf(value)
	if !s.allows(kind) {
		return fmt.Errorf("%s must be %s, got %s", schemaLabel(path), describeSchemaTypes(s.types), kind)
	}

	switch kind {
	case jsonKindObject:
		return s.validateObject(value.(map[string]any), path)
	case jsonKindArray:
		if s.items == nil {
			return nil
		}
		for i, item := range value.([]any) {
			if err := s.items.validate(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *jsonSchema) validateObject(object map[string]any, path string) error {
	for _, name := range s.required {
		if _, ok := object[name]; !ok {
			return fmt.Errorf("missing %s", childSchemaPath(path, name))
		}
	}

	// Sorted so a reply with several problems always names the same one.
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		child, known := s.properties[name]
		if !known {
			if s.additionalProperties {
				continue
			}
			return fmt.Errorf("%s has an unknown property %q", schemaLabel(path), name)
		}
		if err := child.validate(object[name], childSchemaPath(path, name)); err != nil {
			return err
		}
	}
	return nil
}

func (s *jsonSchema) allows(kind string) bool {
	for _, name := range s.types {
		if name == kind {
			return true
		}
	}
	return false
}

func jsonKindOf(value any) string {
	switch value.(type) {
	case nil:
		return jsonKindNull
	case map[string]any:
		return jsonKindObject
	case []any:
		return jsonKindArray
	case string:
		return jsonKindString
	case bool:
		return jsonKindBoolean
	case json.Number, float64:
		return jsonKindNumber
	default:
		return fmt.Sprintf("%T", value)
	}
}

func describeSchemaTypes(types []string) string {
	described := make([]string, 0, len(types))
	for _, name := range types {
		switch name {
		case jsonKindNull:
			described = append(described, "null")
		case jsonKindObject, jsonKindArray:
			described = append(described, "an "+name)
		default:
			described = append(described, "a "+name)
		}
	}
	return strings.Join(described, " or ")
}

func schemaLabel(path string) string {
	if path == "" {
		return "the response"
	}
	return path
}

func childSchemaPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
