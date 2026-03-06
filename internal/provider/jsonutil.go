package provider

import "encoding/json"

// FindFloat searches a map for the first matching key and returns its value as a float64.
// It handles both native float64 values and json.Number values.
func FindFloat(obj map[string]interface{}, keys []string) float64 {
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			switch n := v.(type) {
			case float64:
				return n
			case json.Number:
				if f, err := n.Float64(); err == nil {
					return f
				}
			}
		}
	}
	return 0
}
