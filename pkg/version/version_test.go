package version

import "testing"

func TestDefaultVersion(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("default version = %q, want %q", Version, "dev")
	}
}
