package tools

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestValidateJSONSchema(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["path", "count"],
		"properties": {
			"path": {
				"type": "string",
				"minLength": 1,
				"maxLength": 100
			},
			"count": {
				"type": "integer",
				"minimum": 1,
				"maximum": 50
			},
			"mode": {
				"type": "string",
				"enum": ["read", "write", "append"]
			},
			"tags": {
				"type": "array",
				"items": { "type": "string" },
				"maxItems": 5
			}
		},
		"additionalProperties": false
	}`)

	t.Run("Valid Arguments", func(t *testing.T) {
		args := json.RawMessage(`{
			"path": "/var/log/app.log",
			"count": 10,
			"mode": "read",
			"tags": ["prod", "auth"]
		}`)
		if err := ValidateJSONSchema(schema, args); err != nil {
			t.Fatalf("expected valid arguments to pass, got: %v", err)
		}
	})

	t.Run("Missing Required Property", func(t *testing.T) {
		args := json.RawMessage(`{
			"path": "/var/log/app.log"
		}`)
		err := ValidateJSONSchema(schema, args)
		if err == nil || !errors.Is(err, ErrSchemaValidation) {
			t.Fatalf("expected ErrSchemaValidation for missing required property, got: %v", err)
		}
	})

	t.Run("Wrong Type For Property", func(t *testing.T) {
		args := json.RawMessage(`{
			"path": 12345,
			"count": 10
		}`)
		err := ValidateJSONSchema(schema, args)
		if err == nil || !errors.Is(err, ErrSchemaValidation) {
			t.Fatalf("expected ErrSchemaValidation for wrong type, got: %v", err)
		}
	})

	t.Run("Float For Integer Property", func(t *testing.T) {
		args := json.RawMessage(`{
			"path": "/tmp",
			"count": 10.5
		}`)
		err := ValidateJSONSchema(schema, args)
		if err == nil || !errors.Is(err, ErrSchemaValidation) {
			t.Fatalf("expected ErrSchemaValidation for float count, got: %v", err)
		}
	})

	t.Run("Value Exceeds Maximum", func(t *testing.T) {
		args := json.RawMessage(`{
			"path": "/tmp",
			"count": 100
		}`)
		err := ValidateJSONSchema(schema, args)
		if err == nil || !errors.Is(err, ErrSchemaValidation) {
			t.Fatalf("expected ErrSchemaValidation for exceeding maximum, got: %v", err)
		}
	})

	t.Run("Invalid Enum Value", func(t *testing.T) {
		args := json.RawMessage(`{
			"path": "/tmp",
			"count": 5,
			"mode": "delete"
		}`)
		err := ValidateJSONSchema(schema, args)
		if err == nil || !errors.Is(err, ErrSchemaValidation) {
			t.Fatalf("expected ErrSchemaValidation for invalid enum, got: %v", err)
		}
	})

	t.Run("Disallowed Additional Property", func(t *testing.T) {
		args := json.RawMessage(`{
			"path": "/tmp",
			"count": 5,
			"unknown_field": "surprise"
		}`)
		err := ValidateJSONSchema(schema, args)
		if err == nil || !errors.Is(err, ErrSchemaValidation) {
			t.Fatalf("expected ErrSchemaValidation for additional property, got: %v", err)
		}
	})

	t.Run("Invalid Array Item Type", func(t *testing.T) {
		args := json.RawMessage(`{
			"path": "/tmp",
			"count": 5,
			"tags": ["prod", 999]
		}`)
		err := ValidateJSONSchema(schema, args)
		if err == nil || !errors.Is(err, ErrSchemaValidation) {
			t.Fatalf("expected ErrSchemaValidation for invalid array item, got: %v", err)
		}
	})

	t.Run("Empty Schema Passes Valid JSON", func(t *testing.T) {
		emptySchema := json.RawMessage(`{}`)
		args := json.RawMessage(`{"any":"field"}`)
		if err := ValidateJSONSchema(emptySchema, args); err != nil {
			t.Fatalf("expected empty schema to allow args, got: %v", err)
		}
	})

	t.Run("Nil Schema Passes Valid JSON", func(t *testing.T) {
		if err := ValidateJSONSchema(nil, json.RawMessage(`{"a":1}`)); err != nil {
			t.Fatalf("expected nil schema to allow args, got: %v", err)
		}
	})

	t.Run("Malformed JSON Arguments Fails", func(t *testing.T) {
		err := ValidateJSONSchema(schema, json.RawMessage(`{not-json`))
		if err == nil || !errors.Is(err, ErrSchemaValidation) {
			t.Fatalf("expected ErrSchemaValidation for malformed JSON, got: %v", err)
		}
	})
}
