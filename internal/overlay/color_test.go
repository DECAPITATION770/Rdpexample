package overlay

import "testing"

func TestParseHexColor(t *testing.T) {
	tests := []struct {
		name                string
		in                  string
		wantR, wantG, wantB byte
		wantOK              bool
	}{
		{"with hash", "#ff0080", 0xff, 0x00, 0x80, true},
		{"without hash", "00ff00", 0x00, 0xff, 0x00, true},
		{"uppercase", "#FFFFFF", 0xff, 0xff, 0xff, true},
		{"empty falls back to white", "", 255, 255, 255, false},
		{"too short falls back to white", "#fff", 255, 255, 255, false},
		{"too long falls back to white", "#ffffffff", 255, 255, 255, false},
		{"invalid hex digit falls back to white", "#gggggg", 255, 255, 255, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, g, b, ok := ParseHexColor(tt.in)
			if r != tt.wantR || g != tt.wantG || b != tt.wantB || ok != tt.wantOK {
				t.Fatalf("ParseHexColor(%q) = (%#x,%#x,%#x,%v), want (%#x,%#x,%#x,%v)",
					tt.in, r, g, b, ok, tt.wantR, tt.wantG, tt.wantB, tt.wantOK)
			}
		})
	}
}
