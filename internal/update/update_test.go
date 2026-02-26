package update

import (
	"testing"
)

func TestCheck_findsUpdate(t *testing.T) {
	rel, err := Check("v0.0.1")
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel == nil {
		t.Fatal("expected update, got nil")
	}
	if rel.Version == "" {
		t.Fatal("empty version")
	}
	if rel.URL == "" {
		t.Fatal("empty URL")
	}
	t.Logf("Found update: %s at %s", rel.Version, rel.URL)
}

func TestCheck_devSkipped(t *testing.T) {
	rel, err := Check("dev")
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel != nil {
		t.Fatalf("expected nil for dev, got %+v", rel)
	}
}

func TestCheck_currentVersionUpToDate(t *testing.T) {
	// First get the latest version
	rel, err := Check("v0.0.1")
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel == nil {
		t.Skip("no releases found")
	}

	// Now check with that version — should be nil
	rel2, err := Check(rel.Version)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if rel2 != nil {
		t.Fatalf("expected nil for current version, got %+v", rel2)
	}
}
