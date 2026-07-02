package hotkey

import "testing"

func TestCombo_String(t *testing.T) {
	c := Combo{Ctrl: true, Alt: true, Key: "C"}
	if got := c.String(); got != "Ctrl+Alt+C" {
		t.Fatalf("String() = %q, want %q", got, "Ctrl+Alt+C")
	}
}

func TestParseCombo(t *testing.T) {
	tests := []struct {
		in   string
		want Combo
	}{
		{"Ctrl+Alt+C", Combo{Ctrl: true, Alt: true, Key: "C"}},
		{"Shift+F1", Combo{Shift: true, Key: "F1"}},
		{"M", Combo{Key: "M"}},
	}
	for _, tt := range tests {
		got, err := ParseCombo(tt.in)
		if err != nil {
			t.Fatalf("ParseCombo(%q) error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseCombo(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseCombo_RequiresAKey(t *testing.T) {
	if _, err := ParseCombo("Ctrl+Alt"); err == nil {
		t.Fatal("expected error for modifiers-only combo")
	}
}

func TestCombo_Matches(t *testing.T) {
	configured := Combo{Ctrl: true, Alt: true, Key: "C"}
	pressed := Combo{Ctrl: true, Alt: true, Key: "C"}
	if !configured.Matches(pressed) {
		t.Fatal("expected exact combo to match")
	}
	if configured.Matches(Combo{Ctrl: true, Key: "C"}) {
		t.Fatal("expected missing modifier to not match")
	}
}
