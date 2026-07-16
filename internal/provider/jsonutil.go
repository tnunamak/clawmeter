package provider

import "encoding/json"

// FindFloatPresent distinguishes an explicit numeric zero from an absent or
// malformed field. It accepts both native float64 values and json.Number.
func FindFloatPresent(obj map[string]interface{}, keys []string) (float64, bool) {
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			switch n := v.(type) {
			case float64:
				return n, true
			case json.Number:
				if f, err := n.Float64(); err == nil {
					return f, true
				}
			}
		}
	}
	return 0, false
}
