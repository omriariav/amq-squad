package status

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CheckAdditive reports whether produced is an additive change of golden:
// every object key in golden is present in produced with the same JSON type
// (null in golden accepts any type, since nullable fields are null in the
// fixture). Keys that produced adds are allowed. Arrays are compared
// element-wise against golden's first element, so every element of a
// produced array must carry the fixture element's keys. A removed field, a
// renamed field or a changed type fails.
func CheckAdditive(golden, produced []byte) error {
	var g, p any
	if err := json.Unmarshal(golden, &g); err != nil {
		return fmt.Errorf("golden: %w", err)
	}
	if err := json.Unmarshal(produced, &p); err != nil {
		return fmt.Errorf("produced: %w", err)
	}
	return checkAdditive("$", g, p)
}

func checkAdditive(path string, g, p any) error {
	if g == nil {
		return nil
	}
	switch gv := g.(type) {
	case map[string]any:
		pv, ok := p.(map[string]any)
		if !ok {
			if p == nil {
				return nil // a nullable object (pool, action) may be null
			}
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
			if err := checkAdditive(path+"."+k, gv[k], child); err != nil {
				return err
			}
		}
	case []any:
		pv, ok := p.([]any)
		if !ok {
			if p == nil {
				return nil // an empty list may encode as null in a producer
			}
			return fmt.Errorf("%s: want array, got %s", path, jsonKind(p))
		}
		if len(gv) == 0 {
			return nil
		}
		for i, el := range pv {
			if err := checkAdditive(fmt.Sprintf("%s[%d]", path, i), gv[0], el); err != nil {
				return err
			}
		}
	default:
		if p != nil && jsonKind(g) != jsonKind(p) {
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
