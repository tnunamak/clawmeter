package config

import (
	"testing"
	"time"
)

// A reading stays fresh for two poll intervals plus ReadingSlack, so a single
// failed poll does not make it stale.
func TestReadingMaxAgeIsTwoPollsPlusSlack(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    time.Duration
	}{
		{0, 11 * time.Minute},   // below the floor, so the poll is five minutes
		{300, 11 * time.Minute}, // the default poll
		{600, 21 * time.Minute},
		{3600, 2*time.Hour + time.Minute},
	} {
		if got := ReadingMaxAge(tc.seconds); got != tc.want {
			t.Fatalf("ReadingMaxAge(%d) = %s, want %s", tc.seconds, got, tc.want)
		}
	}
}
