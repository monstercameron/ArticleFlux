package host

import (
	"encoding/json"
	"strconv"
)

// The two preferences the shell keeps, in the same user_prefs table the
// client's own settings live in (store.ReaderRepo.GetPrefs/SetPrefs).
//
// In the database rather than a JSON file beside it, because the database is
// already the one place this application keeps state and both are reachable
// through internal/app's public surface (App.Repo). The client ignores keys it
// does not know — GetPrefs returns a flat map and the client reads the keys it
// asks for — and the `desktop.` prefix keeps them out of anything it does read.
const (
	PrefWindow = "desktop.window"
	PrefPort   = "desktop.port"
)

// Bounds is where the window was when it last moved or closed.
type Bounds struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"w"`
	Height    int  `json:"h"`
	Maximised bool `json:"max,omitempty"`
}

// The window's floor. Below this the three panes stop fitting and the client
// switches to its phone layout, which is not what a desktop window restored
// from a bad value should open in.
const (
	MinWidth  = 720
	MinHeight = 480
	maxSide   = 16384
)

// DefaultBounds is the first-run window: large enough for all three panes at
// their default widths, small enough for a 1366x768 laptop panel.
var DefaultBounds = Bounds{Width: 1280, Height: 800}

// ParseBounds reads the stored value. Anything unreadable or implausible is
// reported as absent rather than repaired, so a corrupt row costs the person a
// re-centred window and not a window opened off-screen at 3 pixels wide.
func ParseBounds(s string) (Bounds, bool) {
	if s == "" {
		return Bounds{}, false
	}
	var b Bounds
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		return Bounds{}, false
	}
	if b.Width < MinWidth || b.Height < MinHeight || b.Width > maxSide || b.Height > maxSide {
		return Bounds{}, false
	}
	if b.X < -maxSide || b.Y < -maxSide || b.X > maxSide || b.Y > maxSide {
		return Bounds{}, false
	}
	return b, true
}

// String is the stored form.
func (b Bounds) String() string {
	out, _ := json.Marshal(b)
	return string(out)
}

// Rect is a screen's work area, in the same coordinates as Bounds.
type Rect struct{ X, Y, Width, Height int }

// Visible reports whether enough of the window's title bar would land on one of
// these screens to grab it. A monitor unplugged since the last run is the
// common way a remembered position ends up somewhere nobody can reach.
func (b Bounds) Visible(screens []Rect) bool {
	const grab = 64 // pixels of title bar that must be on screen
	for _, s := range screens {
		left, right := max(b.X, s.X), min(b.X+b.Width, s.X+s.Width)
		top := b.Y
		if right-left >= grab && top >= s.Y && top < s.Y+s.Height-grab/2 {
			return true
		}
	}
	return false
}

// ParsePort reads the remembered port; zero means none.
func ParsePort(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > 65535 {
		return 0
	}
	return n
}
