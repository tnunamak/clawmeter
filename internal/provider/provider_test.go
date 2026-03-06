package provider

import (
	"testing"
	"time"
)

func TestFailureGate_SuppressesFirstFailure(t *testing.T) {
	g := NewFailureGate()

	// First failure with prior data — suppressed
	if g.ShouldSurfaceError("claude", true) {
		t.Error("first failure with prior data should be suppressed")
	}

	// Second failure — surfaced
	if !g.ShouldSurfaceError("claude", true) {
		t.Error("second failure should be surfaced")
	}
}

func TestFailureGate_SurfacesFirstFailureWithoutPriorData(t *testing.T) {
	g := NewFailureGate()

	if !g.ShouldSurfaceError("claude", false) {
		t.Error("first failure without prior data should be surfaced")
	}
}

func TestFailureGate_SuccessResetsStreak(t *testing.T) {
	g := NewFailureGate()

	g.ShouldSurfaceError("claude", true) // streak=1, suppressed
	g.RecordSuccess("claude")            // reset

	// Next failure is treated as first again
	if g.ShouldSurfaceError("claude", true) {
		t.Error("failure after success reset should be suppressed")
	}
}

func TestFailureGate_PerProvider(t *testing.T) {
	g := NewFailureGate()

	g.ShouldSurfaceError("claude", true) // claude streak=1
	g.ShouldSurfaceError("gemini", true) // gemini streak=1

	// Claude's second failure surfaces
	if !g.ShouldSurfaceError("claude", true) {
		t.Error("claude second failure should surface")
	}
	// Gemini's second failure surfaces independently
	if !g.ShouldSurfaceError("gemini", true) {
		t.Error("gemini second failure should surface")
	}
}

func TestFailureGate_BackoffGrows(t *testing.T) {
	g := NewFailureGate()

	g.ShouldSurfaceError("claude", true)  // backoff = 5m
	g.ShouldSurfaceError("claude", true)  // backoff = 10m
	g.ShouldSurfaceError("claude", true)  // backoff = 20m
	g.ShouldSurfaceError("claude", true)  // backoff = 30m (cap)
	g.ShouldSurfaceError("claude", true)  // backoff = 30m (stays capped)

	if g.backoffs["claude"] != maxBackoff {
		t.Errorf("backoff = %v, want %v", g.backoffs["claude"], maxBackoff)
	}
}

func TestFailureGate_InBackoff(t *testing.T) {
	g := NewFailureGate()

	if g.InBackoff("claude") {
		t.Error("should not be in backoff initially")
	}

	g.ShouldSurfaceError("claude", false) // sets nextPoll ~5m from now

	if !g.InBackoff("claude") {
		t.Error("should be in backoff after failure")
	}

	// Simulate time passing by directly setting nextPoll to the past
	g.nextPoll["claude"] = time.Now().Add(-1 * time.Second)

	if g.InBackoff("claude") {
		t.Error("should not be in backoff after time passes")
	}
}

func TestFailureGate_SuccessResetsBackoff(t *testing.T) {
	g := NewFailureGate()

	g.ShouldSurfaceError("claude", false) // sets backoff
	if !g.InBackoff("claude") {
		t.Fatal("should be in backoff")
	}

	g.RecordSuccess("claude")

	if g.InBackoff("claude") {
		t.Error("backoff should be cleared after success")
	}
}
