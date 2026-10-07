package webhookpayload

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed schemas/*.json
var schemaFS embed.FS

// ListEventSchemaTypes returns known Standard Webhooks event type ids.
func ListEventSchemaTypes() []string {
	return []string{
		TypeTransactionTRXReceived,
		TypeTransactionTRXBroadcasted,
		TypeTransactionTRC20Received,
		TypeTransactionTRC20Broadcasted,
	}
}

// EventSchemaJSON returns the JSON Schema document for an event type.
func EventSchemaJSON(eventType string) ([]byte, error) {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return nil, fmt.Errorf("event type is required")
	}
	if !IsKnownEventType(eventType) {
		return nil, fmt.Errorf("unknown event type %q", eventType)
	}
	path := "schemas/" + eventType + ".schema.json"
	raw, err := schemaFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unknown event type %q", eventType)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid schema file %s", path)
	}
	return raw, nil
}

// AllEventSchemas returns a map of event type to parsed JSON Schema.
func AllEventSchemas() (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage)
	for _, t := range ListEventSchemaTypes() {
		raw, err := EventSchemaJSON(t)
		if err != nil {
			return nil, err
		}
		out[t] = raw
	}
	return out, nil
}

// SchemaFS exposes embedded schema files for static serving.
func SchemaFS() fs.FS {
	sub, err := fs.Sub(schemaFS, "schemas")
	if err != nil {
		return schemaFS
	}
	return sub
}
