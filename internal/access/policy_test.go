package access_test

import (
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func TestHighest(t *testing.T) {
	tests := []struct {
		name   string
		levels []access.Level
		want   access.Level
	}{
		{"no grants means none", nil, access.LevelNone},
		{"single view", []access.Level{access.LevelView}, access.LevelView},
		{"single edit", []access.Level{access.LevelEdit}, access.LevelEdit},
		{"edit beats view", []access.Level{access.LevelView, access.LevelEdit}, access.LevelEdit},
		{"order does not matter", []access.Level{access.LevelEdit, access.LevelView}, access.LevelEdit},
		{"duplicates", []access.Level{access.LevelView, access.LevelView}, access.LevelView},
		{"none never raises", []access.Level{access.LevelNone, access.LevelView}, access.LevelView},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := access.Highest(tc.levels...); got != tc.want {
				t.Errorf("Highest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	valid := map[string]access.Level{"view": access.LevelView, "edit": access.LevelEdit}
	for in, want := range valid {
		got, err := access.ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}

	for _, in := range []string{"", "admin", "VIEW", "none"} {
		if got, err := access.ParseLevel(in); err == nil || got != access.LevelNone {
			t.Errorf("ParseLevel(%q) = %v, %v; want LevelNone and an error", in, got, err)
		}
	}
}
