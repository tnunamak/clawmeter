package provider

import "testing"

func TestFindFloatPresentDistinguishesZeroFromAbsence(t *testing.T) {
	value, ok := FindFloatPresent(map[string]interface{}{"usage": float64(0)}, []string{"usage"})
	if !ok || value != 0 {
		t.Fatalf("explicit zero = %v, %t; want 0, true", value, ok)
	}
	if value, ok = FindFloatPresent(map[string]interface{}{}, []string{"usage"}); ok || value != 0 {
		t.Fatalf("missing value = %v, %t; want 0, false", value, ok)
	}
}
