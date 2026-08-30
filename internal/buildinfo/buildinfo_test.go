// Tests for the minimum-version comparison (internal/buildinfo).
package buildinfo

import "testing"

func TestAtLeast(t *testing.T) {
	cases := []struct {
		current string
		minimum string
		want    bool
	}{
		{"0.2.0", "0.2.0", true},
		{"0.2.1", "0.2.0", true},
		{"1.0.0", "0.2.0", true},
		{"0.2.0", "0.2.1", false},
		{"0.1.0", "0.2.0", false},
		{"0.2.0-beta.1", "0.2.0", true},  // prerelease ignored
		{"0.2.0+build.5", "0.2.0", true}, // build metadata ignored
		{"0.2", "0.2.0", true},           // missing segment treated as zero
		{"0.10.0", "0.9.0", true},        // numeric, not lexicographic
		{"bad", "0.2.0", false},
	}
	for _, c := range cases {
		if got := AtLeast(c.current, c.minimum); got != c.want {
			t.Errorf("AtLeast(%q, %q) = %v, want %v", c.current, c.minimum, got, c.want)
		}
	}
}

func TestParts(t *testing.T) {
	if got := parts("1.2.3"); got != [3]int{1, 2, 3} {
		t.Errorf("parts(1.2.3) = %v", got)
	}
	if got := parts("1"); got != [3]int{1, 0, 0} {
		t.Errorf("parts(1) = %v", got)
	}
	if got := parts(""); got != [3]int{0, 0, 0} {
		t.Errorf("parts('') = %v", got)
	}
}
