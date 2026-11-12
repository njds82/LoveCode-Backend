package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
)

// SchemaNode represents a parsed JSON Schema definition.
type SchemaNode struct {
	Type                 any                    `json:"type"`
	Properties           map[string]*SchemaNode `json:"properties"`
	Required             []string               `json:"required"`
	AdditionalProperties any                    `json:"additionalProperties"`
	Items                *SchemaNode            `json:"items"`
	Enum                 []any                  `json:"enum"`
	Minimum              *float64               `json:"minimum"`
	Maximum              *float64               `json:"maximum"`
	MinLength            *int                   `json:"minLength"`
	MaxLength            *int                   `json:"maxLength"`
	MinItems             *int                   `json:"minItems"`
	MaxItems             *int                   `json:"maxItems"`
}

// ValidateJSONSchema performs pure Go recursive zero-dependency JSON schema validation.
func ValidateJSONSchema(schemaRaw, argsRaw json.RawMessage) error {
	trimmedSchema := bytes.TrimSpace(schemaRaw)
	if len(trimmedSchema) == 0 || string(trimmedSchema) == "{}" || string(trimmedSchema) == "null" {
		return nil
	}

	var schema SchemaNode
	if err := json.Unmarshal(trimmedSchema, &schema); err != nil {
		return fmt.Errorf("%w: invalid schema JSON: %v", ErrSchemaValidation, err)
	}

	trimmedArgs := bytes.TrimSpace(argsRaw)
	var data any
	if len(trimmedArgs) == 0 || string(trimmedArgs) == "null" {
		// If empty arguments are passed, default to empty object
		data = map[string]any{}
	} else {
		if err := json.Unmarshal(trimmedArgs, &data); err != nil {
			return fmt.Errorf("%w: arguments must be valid JSON: %v", ErrSchemaValidation, err)
		}
	}

	return validateNode(&schema, data, "")
}

func validateNode(s *SchemaNode, val any, path string) error {
	if s == nil {
		return nil
	}

	// 1. Enum check
	if len(s.Enum) > 0 {
		matched := false
		for _, allowed := range s.Enum {
			if matchValues(allowed, val) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w: %s value %v is not one of allowed enum values", ErrSchemaValidation, prefix(path), val)
		}
	}

	// 2. Type check
	if s.Type != nil {
		if err := checkType(s, val, path); err != nil {
			return err
		}
	}

	// 3. String length constraints
	if str, ok := val.(string); ok {
		if s.MinLength != nil && len(str) < *s.MinLength {
			return fmt.Errorf("%w: %s length %d is less than minLength %d", ErrSchemaValidation, prefix(path), len(str), *s.MinLength)
		}
		if s.MaxLength != nil && len(str) > *s.MaxLength {
			return fmt.Errorf("%w: %s length %d exceeds maxLength %d", ErrSchemaValidation, prefix(path), len(str), *s.MaxLength)
		}
	}

	// 4. Numeric constraints
	if num, ok := toFloat(val); ok {
		if s.Minimum != nil && num < *s.Minimum {
			return fmt.Errorf("%w: %s value %g is less than minimum %g", ErrSchemaValidation, prefix(path), num, *s.Minimum)
		}
		if s.Maximum != nil && num > *s.Maximum {
			return fmt.Errorf("%w: %s value %g exceeds maximum %g", ErrSchemaValidation, prefix(path), num, *s.Maximum)
		}
	}

	// 5. Array constraints & items
	if arr, ok := val.([]any); ok {
		if s.MinItems != nil && len(arr) < *s.MinItems {
			return fmt.Errorf("%w: %s array length %d is less than minItems %d", ErrSchemaValidation, prefix(path), len(arr), *s.MinItems)
		}
		if s.MaxItems != nil && len(arr) > *s.MaxItems {
			return fmt.Errorf("%w: %s array length %d exceeds maxItems %d", ErrSchemaValidation, prefix(path), len(arr), *s.MaxItems)
		}
		if s.Items != nil {
			for i, item := range arr {
				itemPath := fmt.Sprintf("%s[%d]", path, i)
				if err := validateNode(s.Items, item, itemPath); err != nil {
					return err
				}
			}
		}
	}

	// 6. Object constraints & properties
	if obj, ok := val.(map[string]any); ok {
		// Required properties
		for _, req := range s.Required {
			if _, exists := obj[req]; !exists {
				return fmt.Errorf("%w: %s missing required property %q", ErrSchemaValidation, prefix(path), req)
			}
		}

		// Property schemas
		for prop, subSchema := range s.Properties {
			if propVal, exists := obj[prop]; exists {
				propPath := prop
				if path != "" {
					propPath = path + "." + prop
				}
				if err := validateNode(subSchema, propVal, propPath); err != nil {
					return err
				}
			}
		}

		// AdditionalProperties check
		if s.AdditionalProperties != nil {
			switch ap := s.AdditionalProperties.(type) {
			case bool:
				if !ap {
					for key := range obj {
						if _, known := s.Properties[key]; !known {
							return fmt.Errorf("%w: %s unexpected additional property %q", ErrSchemaValidation, prefix(path), key)
						}
					}
				}
			case map[string]any:
				// Sub-schema for additional properties
				var apNode SchemaNode
				b, _ := json.Marshal(ap)
				if err := json.Unmarshal(b, &apNode); err == nil {
					for key, v := range obj {
						if _, known := s.Properties[key]; !known {
							propPath := key
							if path != "" {
								propPath = path + "." + key
							}
							if err := validateNode(&apNode, v, propPath); err != nil {
								return err
							}
						}
					}
				}
			}
		}
	}

	return nil
}

func checkType(s *SchemaNode, val any, path string) error {
	var expectedTypes []string
	switch t := s.Type.(type) {
	case string:
		expectedTypes = []string{t}
	case []any:
		for _, item := range t {
			if str, ok := item.(string); ok {
				expectedTypes = append(expectedTypes, str)
			}
		}
	}

	if len(expectedTypes) == 0 {
		return nil
	}

	for _, exp := range expectedTypes {
		if matchesType(exp, val) {
			return nil
		}
	}

	return fmt.Errorf("%w: %s expected type %v, got %T", ErrSchemaValidation, prefix(path), s.Type, val)
}

func matchesType(expected string, val any) bool {
	if val == nil {
		return expected == "null"
	}
	switch expected {
	case "string":
		_, ok := val.(string)
		return ok
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "number":
		_, ok := toFloat(val)
		return ok
	case "integer":
		if f, ok := toFloat(val); ok {
			return f == math.Trunc(f)
		}
		return false
	case "array":
		_, ok := val.([]any)
		return ok
	case "object":
		_, ok := val.(map[string]any)
		return ok
	case "null":
		return val == nil
	default:
		return true
	}
}

func toFloat(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	default:
		return 0, false
	}
}

func matchValues(a, b any) bool {
	if a == b {
		return true
	}
	fa, oka := toFloat(a)
	fb, okb := toFloat(b)
	if oka && okb {
		return fa == fb
	}
	return reflect.DeepEqual(a, b)
}

func prefix(path string) string {
	if path == "" {
		return "root:"
	}
	return fmt.Sprintf("property %q:", path)
}
