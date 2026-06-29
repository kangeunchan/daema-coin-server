package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

func mapFromStruct(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeMap(item map[string]any, target any) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func generatedID(prefix string) string {
	prefix = strings.Trim(prefix, "-_ .")
	if prefix == "" {
		prefix = "resource"
	}
	token := randomToken()
	if len(token) > 16 {
		token = token[:16]
	}
	return fmt.Sprintf("%s-%s", prefix, token)
}

func resourceIDPrefix(domain string) string {
	parts := strings.FieldsFunc(domain, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return "resource"
	}
	last := parts[len(parts)-1]
	if strings.HasSuffix(last, "ies") && len(last) > 3 {
		return last[:len(last)-3] + "y"
	}
	return strings.TrimSuffix(last, "s")
}
