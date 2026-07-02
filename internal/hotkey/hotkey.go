package hotkey

import (
	"errors"
	"strings"
)

// Combo is a normalized hotkey representation: a set of modifier flags
// plus exactly one non-modifier key. It is intentionally OS-agnostic —
// the Viewer's Fyne key handler maps *fyne.KeyEvent into a Combo, and the
// same struct is persisted to disk as its String() form.
type Combo struct {
	Ctrl  bool
	Alt   bool
	Shift bool
	Key   string // canonical key name, e.g. "C", "F1"
}

func (c Combo) String() string {
	var parts []string
	if c.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if c.Alt {
		parts = append(parts, "Alt")
	}
	if c.Shift {
		parts = append(parts, "Shift")
	}
	parts = append(parts, c.Key)
	return strings.Join(parts, "+")
}

func (c Combo) Matches(pressed Combo) bool {
	return c == pressed
}

func ParseCombo(s string) (Combo, error) {
	parts := strings.Split(s, "+")
	if len(parts) == 0 {
		return Combo{}, errors.New("hotkey: empty combo")
	}
	var c Combo
	key := parts[len(parts)-1]
	if key == "" {
		return Combo{}, errors.New("hotkey: missing key")
	}
	if key == "Ctrl" || key == "Alt" || key == "Shift" {
		return Combo{}, errors.New("hotkey: combo must end in a non-modifier key")
	}
	c.Key = key
	for _, mod := range parts[:len(parts)-1] {
		switch mod {
		case "Ctrl":
			c.Ctrl = true
		case "Alt":
			c.Alt = true
		case "Shift":
			c.Shift = true
		default:
			return Combo{}, errors.New("hotkey: unknown modifier " + mod)
		}
	}
	return c, nil
}
