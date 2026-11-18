package openaicredits

import "testing"

// The extension hashes the same prefix with crypto.subtle (openai-observer.js);
// this value is its output for "org-personal".
func TestPoolIDMatchesTheExtension(t *testing.T) {
	if got := PoolID("org-personal"); got != "a6c2ecf46ee880a3644e48bfff82795ded277aff2847b0b21dda3dcd8d44c30c" {
		t.Fatalf("PoolID = %s", got)
	}
}
