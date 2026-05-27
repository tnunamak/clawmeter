package forecast

import (
	"strings"
	"testing"
	"time"
)

func TestProject(t *testing.T) {
	// Project calls time.Until(resetsAt) internally, so there will be
	// microsecond drift between our "now" and the one inside Project.
	// We use generous bounds and avoid testing exact boundary conditions
	// that flip on sub-millisecond timing differences.
	now := time.Now()

	tests := []struct {
		name       string
		currentPct float64
		resetsAt   time.Time
		windowLen  time.Duration
		// Expected (approximate) values:
		wantOnTrack         bool
		wantWillLastToReset bool
		wantProjectedLo     float64 // projected pct lower bound (inclusive)
		wantProjectedHi     float64 // projected pct upper bound (inclusive)
		wantRunsOutPositive bool    // true if RunsOutIn should be > 0
		wantRunsOutEarly    bool    // true if RunsOutEarlyBy should be > 0
	}{
		{
			name:                "0% usage, 3h remaining of 5h window → no projected spend",
			currentPct:          0,
			resetsAt:            now.Add(3 * time.Hour),
			windowLen:           FiveHourWindow,
			wantOnTrack:         true,
			wantWillLastToReset: true,
			wantProjectedLo:     0,
			wantProjectedHi:     0,
		},
		{
			name:                "25% usage, 2.5h remaining of 5h window → well under pace",
			currentPct:          25,
			resetsAt:            now.Add(150 * time.Minute),
			windowLen:           FiveHourWindow,
			wantOnTrack:         true,
			wantWillLastToReset: true,
			wantProjectedLo:     49,
			wantProjectedHi:     51,
		},
		{
			name:                "95% usage, 2h remaining of 5h window → projected to run out",
			currentPct:          95,
			resetsAt:            now.Add(2 * time.Hour),
			windowLen:           FiveHourWindow,
			wantOnTrack:         false,
			wantWillLastToReset: false,
			wantProjectedLo:     155,
			wantProjectedHi:     160,
			wantRunsOutPositive: true,
			wantRunsOutEarly:    true,
		},
		{
			name:                "100% usage, 1h remaining of 5h window → maxed out",
			currentPct:          100,
			resetsAt:            now.Add(1 * time.Hour),
			windowLen:           FiveHourWindow,
			wantOnTrack:         false,
			wantWillLastToReset: false, // at 100%, secsToExhaust=0 which is < remaining
			wantProjectedLo:     124,
			wantProjectedHi:     126,
			wantRunsOutEarly:    true,
		},
		{
			name:                "over 100% usage clamps run-out to now",
			currentPct:          105,
			resetsAt:            now.Add(1 * time.Hour),
			windowLen:           FiveHourWindow,
			wantOnTrack:         false,
			wantWillLastToReset: false,
			wantProjectedLo:     130,
			wantProjectedHi:     132,
			wantRunsOutEarly:    true,
		},
		{
			name:                "0% usage, 0h remaining — just reset edge case",
			currentPct:          0,
			resetsAt:            now,
			windowLen:           FiveHourWindow,
			wantOnTrack:         true,
			wantWillLastToReset: true,
			wantProjectedLo:     0,
			wantProjectedHi:     0,
		},
		{
			name:                "50% usage with most of window elapsed → projected ~62.5%",
			currentPct:          50,
			resetsAt:            now.Add(1 * time.Hour),
			windowLen:           FiveHourWindow,
			wantOnTrack:         true, // 62.5% < 100
			wantWillLastToReset: true, // secsToExhaust = 50/rate = 14400s > 3600s remaining
			wantProjectedLo:     61,
			wantProjectedHi:     64,
		},
		{
			name:                "10% usage early in 7d window → healthy",
			currentPct:          10,
			resetsAt:            now.Add(6 * 24 * time.Hour),
			windowLen:           SevenDayWindow,
			wantOnTrack:         true,
			wantWillLastToReset: true,
			wantProjectedLo:     60,
			wantProjectedHi:     80,
		},
		{
			name:                "elapsed is negative (resetsAt beyond window) → early return",
			currentPct:          50,
			resetsAt:            now.Add(6 * time.Hour),
			windowLen:           FiveHourWindow,
			wantOnTrack:         true,
			wantWillLastToReset: true,
			wantProjectedLo:     50,
			wantProjectedHi:     50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proj := Project(tt.currentPct, tt.resetsAt, tt.windowLen)

			if proj.OnTrack != tt.wantOnTrack {
				t.Errorf("OnTrack = %v, want %v (projected=%.2f)", proj.OnTrack, tt.wantOnTrack, proj.ProjectedPct)
			}
			if proj.WillLastToReset != tt.wantWillLastToReset {
				t.Errorf("WillLastToReset = %v, want %v (projected=%.2f)", proj.WillLastToReset, tt.wantWillLastToReset, proj.ProjectedPct)
			}
			if proj.ProjectedPct < tt.wantProjectedLo || proj.ProjectedPct > tt.wantProjectedHi {
				t.Errorf("ProjectedPct = %.2f, want [%.0f, %.0f]", proj.ProjectedPct, tt.wantProjectedLo, tt.wantProjectedHi)
			}
			if tt.wantRunsOutPositive && proj.RunsOutIn <= 0 {
				t.Errorf("RunsOutIn = %v, want > 0", proj.RunsOutIn)
			}
			if !tt.wantRunsOutPositive && proj.RunsOutIn != 0 {
				t.Errorf("RunsOutIn = %v, want 0", proj.RunsOutIn)
			}
			if tt.wantRunsOutEarly && proj.RunsOutEarlyBy <= 0 {
				t.Errorf("RunsOutEarlyBy = %v, want > 0", proj.RunsOutEarlyBy)
			}
			if !tt.wantRunsOutEarly && proj.RunsOutEarlyBy != 0 {
				t.Errorf("RunsOutEarlyBy = %v, want 0", proj.RunsOutEarlyBy)
			}
		})
	}
}

func TestGuessWindowType(t *testing.T) {
	tests := []struct {
		name string
		want time.Duration
	}{
		{"5h", FiveHourWindow},
		{"7d", SevenDayWindow},
		{"7d-opus", SevenDayWindow},
		{"7d_oauth_apps", SevenDayWindow},
		{"session", 24 * time.Hour}, // default
		{"credits", 24 * time.Hour}, // default
		{"monthly", 24 * time.Hour}, // default
		{"", 24 * time.Hour},        // default
	}

	for _, tt := range tests {
		t.Run("name="+tt.name, func(t *testing.T) {
			got := GuessWindowType(tt.name)
			if got != tt.want {
				t.Errorf("GuessWindowType(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestProject_RunOutEarlyByIsRelativeToReset(t *testing.T) {
	now := time.Now()
	proj := Project(75, now.Add(time.Hour), 2*time.Hour)

	if proj.WillLastToReset {
		t.Fatal("WillLastToReset = true, want false")
	}
	if proj.RunsOutIn < 19*time.Minute || proj.RunsOutIn > 21*time.Minute {
		t.Errorf("RunsOutIn = %v, want about 20m from now", proj.RunsOutIn)
	}
	if proj.RunsOutEarlyBy < 39*time.Minute || proj.RunsOutEarlyBy > 41*time.Minute {
		t.Errorf("RunsOutEarlyBy = %v, want about 40m before reset", proj.RunsOutEarlyBy)
	}
}

func TestProject_CurrentPctOverLimitDoesNotProduceNegativeRunOut(t *testing.T) {
	proj := Project(105, time.Now().Add(time.Hour), FiveHourWindow)

	if proj.RunsOutIn != 0 {
		t.Errorf("RunsOutIn = %v, want 0 because quota is already exhausted", proj.RunsOutIn)
	}
	if proj.RunsOutEarlyBy < 59*time.Minute || proj.RunsOutEarlyBy > time.Hour {
		t.Errorf("RunsOutEarlyBy = %v, want about the full remaining time", proj.RunsOutEarlyBy)
	}
}

func TestProjection_Indicator(t *testing.T) {
	tests := []struct {
		projected float64
		want      string
	}{
		{0, "0%"},
		{50.4, "50%"},
		{99.6, "100%"},
		{150, "150%"},
	}

	for _, tt := range tests {
		p := Projection{ProjectedPct: tt.projected}
		got := p.Indicator()
		if got != tt.want {
			t.Errorf("Projection{ProjectedPct: %v}.Indicator() = %q, want %q", tt.projected, got, tt.want)
		}
	}
}

func TestProjection_PaceIndicator(t *testing.T) {
	tests := []struct {
		name string
		proj Projection
		want string // substring match
	}{
		{
			name: "under limit estimate",
			proj: Projection{ProjectedPct: 70, WillLastToReset: true},
			want: "est. 70% at reset",
		},
		{
			name: "over limit estimate with run-out note",
			proj: Projection{ProjectedPct: 139, WillLastToReset: false, RunsOutEarlyBy: 2 * time.Hour},
			want: "est. 139% at reset · runs out 2h00m early",
		},
		{
			name: "zero usage estimate",
			proj: Projection{ProjectedPct: 0, WillLastToReset: true},
			want: "est. 0% at reset",
		},
		{
			name: "near limit estimate",
			proj: Projection{ProjectedPct: 98, WillLastToReset: true},
			want: "est. 98% at reset",
		},
		{
			name: "over limit estimate",
			proj: Projection{ProjectedPct: 104, WillLastToReset: false},
			want: "est. 104% at reset",
		},
		{
			name: "runs out includes duration",
			proj: Projection{ProjectedPct: 128, WillLastToReset: false, RunsOutEarlyBy: 90 * time.Minute},
			want: "runs out 1h30m early",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.proj.PaceIndicator()
			if !strings.Contains(got, tt.want) {
				t.Errorf("PaceIndicator() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

func TestPaceIndicatorAlignment(t *testing.T) {
	// Run-out notes keep a fixed-width estimate before the extra note so
	// columns align in CLI output.
	cases := []Projection{
		{ProjectedPct: 99, WillLastToReset: false, RunsOutEarlyBy: 5 * time.Minute},
		{ProjectedPct: 104, WillLastToReset: false, RunsOutEarlyBy: 2 * time.Hour},
		{ProjectedPct: 139, WillLastToReset: false, RunsOutEarlyBy: 18 * time.Hour},
	}

	for _, proj := range cases {
		got := proj.PaceIndicator()
		parts := strings.SplitN(got, " · ", 2)
		if len(parts) != 2 {
			t.Fatalf("PaceIndicator() = %q, want a run-out note", got)
		}
		left := parts[0]
		if len(left) != paceWidth {
			t.Errorf("PaceIndicator() left %q has len %d, want %d",
				left, len(left), paceWidth)
		}
	}
}

func TestPaceIndicatorNoLastsToResetFiller(t *testing.T) {
	got := (Projection{ProjectedPct: 70, WillLastToReset: true}).PaceIndicator()
	if strings.Contains(got, "lasts to reset") || strings.Contains(got, " · ") {
		t.Errorf("PaceIndicator() = %q, want only the reset estimate", got)
	}
}
