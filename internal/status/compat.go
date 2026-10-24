package status

import (
	"encoding/json"
	"fmt"
	"sort"
)

// NullableFields are the schema 1 fields whose value may be null (their
// golden value may be a value or null). Everywhere else, null in the
// produced document is a removal: a required object, list or scalar replaced
// by null fails, and so does a null document.
var NullableFields = map[string]bool{
	"last_activity_at": true, "last_message_at": true, "tokens_in": true, "tokens_out": true, // seat
	"pool": true, "lease_expires_at": true, // task
	"action":            true,                            // attention
	"usage_limit_since": true, "usage_limit_until": true, // scheduler
}

// CheckAdditive reports whether produced is an additive change of golden:
// every object key in golden is present in produced with the same JSON type.
// Null is accepted only at a NullableFields key; a null golden value accepts
// any type. Keys that produced adds are allowed. Arrays are compared
// element-wise against golden's first element, so every element of a
// produced array must carry the fixture element's keys. A removed field, a
// renamed field, a changed type or a required value replaced by null fails.
func CheckAdditive(golden, produced []byte) error {
	var g, p any
	if err := json.Unmarshal(golden, &g); err != nil {
		return fmt.Errorf("golden: %w", err)
	}
	if err := json.Unmarshal(produced, &p); err != nil {
		return fmt.Errorf("produced: %w", err)
	}
	if _, ok := p.(map[string]any); !ok {
		return fmt.Errorf("$: want the contract object, got %s", jsonKind(p))
	}
	return checkAdditive("$", "", g, p)
}

func checkAdditive(path, key string, g, p any) error {
	if g == nil {
		return nil
	}
	if p == nil {
		if NullableFields[key] {
			return nil
		}
		return fmt.Errorf("%s: required %s replaced by null", path, jsonKind(g))
	}
	switch gv := g.(type) {
	case map[string]any:
		pv, ok := p.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want object, got %s", path, jsonKind(p))
		}
		keys := make([]string, 0, len(gv))
		for k := range gv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child, ok := pv[k]
			if !ok {
				return fmt.Errorf("%s.%s: field removed", path, k)
			}
			if err := checkAdditive(path+"."+k, k, gv[k], child); err != nil {
				return err
			}
		}
	case []any:
		pv, ok := p.([]any)
		if !ok {
			return fmt.Errorf("%s: want array, got %s", path, jsonKind(p))
		}
		if len(gv) == 0 {
			return nil
		}
		for i, el := range pv {
			if err := checkAdditive(fmt.Sprintf("%s[%d]", path, i), "", gv[0], el); err != nil {
				return err
			}
		}
	default:
		if jsonKind(g) != jsonKind(p) {
			return fmt.Errorf("%s: type changed from %s to %s", path, jsonKind(g), jsonKind(p))
		}
	}
	return nil
}

func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}
