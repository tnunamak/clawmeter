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
	}{
		{
			name:                "0% usage, 3h remaining of 5h window → on pace",
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
			name:                "95% usage, 2h remaining of 5h window → ahead of pace",
			currentPct:          95,
			resetsAt:            now.Add(2 * time.Hour),
			windowLen:           FiveHourWindow,
			wantOnTrack:         false,
			wantWillLastToReset: false,
			wantProjectedLo:     155,
			wantProjectedHi:     160,
			wantRunsOutPositive: true,
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
		{"session", 24 * time.Hour},    // default
		{"credits", 24 * time.Hour},    // default
		{"monthly", 24 * time.Hour},    // default
		{"", 24 * time.Hour},           // default
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
			name: "on pace and lasts",
			proj: Projection{Delta: 1.0, WillLastToReset: true},
			want: "on pace",
		},
		{
			name: "behind with runs out",
			proj: Projection{Delta: 15, WillLastToReset: false, RunsOutIn: 2 * time.Hour},
			want: "15% behind",
		},
		{
			name: "ahead and lasts",
			proj: Projection{Delta: -20, WillLastToReset: true},
			want: "20% ahead",
		},
		{
			name: "on pace boundary (delta=2)",
			proj: Projection{Delta: 2, WillLastToReset: true},
			want: "on pace",
		},
		{
			name: "just beyond on pace (delta=3)",
			proj: Projection{Delta: 3, WillLastToReset: true},
			want: "3% behind",
		},
		{
			name: "runs out includes duration",
			proj: Projection{Delta: 10, WillLastToReset: false, RunsOutIn: 90 * time.Minute},
			want: "runs out in 1h30m",
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

func TestShortDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Minute, "in 5m"},
		{90 * time.Minute, "in 1h30m"},
		{25 * time.Hour, "in 1d1h"},
		{7 * 24 * time.Hour, "in 7d0h"},
	}

	for _, tt := range tests {
		got := shortDuration(tt.d)
		if got != tt.want {
			t.Errorf("shortDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
