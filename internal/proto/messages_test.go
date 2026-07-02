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
