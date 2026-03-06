package format

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{
			name: "zero duration",
			d:    0,
			want: "0m",
		},
		{
			name: "30 seconds rounds to 1m (Round rounds .5 up)",
			d:    30 * time.Second,
			want: "1m",
		},
		{
			name: "29 seconds rounds down to 0m",
			d:    29 * time.Second,
			want: "0m",
		},
		{
			name: "65 minutes",
			d:    65 * time.Minute,
			want: "1h05m",
		},
		{
			name: "25 hours",
			d:    25 * time.Hour,
			want: "1d1h",
		},
		{
			name: "7 days exactly",
			d:    7 * 24 * time.Hour,
			want: "7d0h",
		},
		{
			name: "negative duration returns now",
			d:    -5 * time.Minute,
			want: "now",
		},
		{
			name: "exactly 1 hour",
			d:    time.Hour,
			want: "1h00m",
		},
		{
			name: "1 minute",
			d:    time.Minute,
			want: "1m",
		},
		{
			name: "59 minutes",
			d:    59 * time.Minute,
			want: "59m",
		},
		{
			name: "90 seconds rounds to 2m",
			d:    90 * time.Second,
			want: "2m",
		},
		{
			name: "1 day exactly",
			d:    24 * time.Hour,
			want: "1d0h",
		},
		{
			name: "large negative",
			d:    -100 * time.Hour,
			want: "now",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDuration(tt.d)
			if got != tt.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}
