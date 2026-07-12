// internal/proto/messages_test.go
package proto

import "testing"

func TestEncodeDecodeFrame_RoundTrip(t *testing.T) {
	original := InputEvent{
		Kind: InputMouseMove,
		X:    100,
		Y:    250,
	}
	encoded, err := EncodeFrame(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	got, ok := decoded.(InputEvent)
	if !ok {
		t.Fatalf("decoded type = %T, want InputEvent", decoded)
	}
	if got != original {
		t.Fatalf("got %+v, want %+v", got, original)
	}
}

func TestOverlayMessage_ValidateFadeSeconds(t *testing.T) {
	tests := []struct {
		name    string
		fade    float64
		wantErr bool
	}{
		{"default", 2.0, false},
		{"one decimal", 3.5, false},
		{"zero", 0.0, true},
		{"negative", -1.0, true},
		{"too many decimals gets rounded not rejected", 2.34, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := OverlayMessage{Text: "hi", FadeSeconds: tt.fade}
			err := msg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOverlayMessage_RoundsFadeToOneDecimal(t *testing.T) {
	msg := OverlayMessage{Text: "hi", FadeSeconds: 2.347}
	msg.Normalize()
	if msg.FadeSeconds != 2.3 {
		t.Fatalf("FadeSeconds = %v, want 2.3", msg.FadeSeconds)
	}
}

func intp(v int) *int { return &v }

func TestSettings_Clamp(t *testing.T) {
	cases := []struct {
		name              string
		in                Settings
		wantFPS, wantQ    *int
		wantMaxW          *int
	}{
		{"nil fields stay nil", Settings{}, nil, nil, nil},
		{"in-range untouched", Settings{FPS: intp(30), Quality: intp(75), MaxWidth: intp(1280)}, intp(30), intp(75), intp(1280)},
		{"fps floored", Settings{FPS: intp(0)}, intp(1), nil, nil},
		{"fps capped", Settings{FPS: intp(999)}, intp(60), nil, nil},
		{"quality floored", Settings{Quality: intp(1)}, nil, intp(10), nil},
		{"quality capped", Settings{Quality: intp(500)}, nil, intp(100), nil},
		{"maxwidth 0 stays native", Settings{MaxWidth: intp(0)}, nil, nil, intp(0)},
		{"maxwidth floored", Settings{MaxWidth: intp(50)}, nil, nil, intp(320)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.in
			s.Clamp()
			eq := func(got, want *int) bool {
				if got == nil || want == nil {
					return got == nil && want == nil
				}
				return *got == *want
			}
			if !eq(s.FPS, c.wantFPS) || !eq(s.Quality, c.wantQ) || !eq(s.MaxWidth, c.wantMaxW) {
				t.Errorf("Clamp() = {FPS:%v Quality:%v MaxWidth:%v}", s.FPS, s.Quality, s.MaxWidth)
			}
		})
	}
}
