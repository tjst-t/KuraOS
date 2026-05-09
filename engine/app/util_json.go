package app

import (
	"encoding/json"
)

// mapToJSON serialises a string map to canonical JSON. Empty input becomes
// "{}" so the SQLite column is never NULL — keeps the read path simpler.
func mapToJSON(m map[string]string) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jsonToMap is the inverse. Empty / unparseable input returns an empty map
// rather than nil so the caller can index without nil-checks.
func jsonToMap(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
