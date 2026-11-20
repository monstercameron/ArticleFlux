package feed

import "testing"

func TestStripBylineDropsALeadingBy(t *testing.T) {
	for in, want := range map[string]string{
		"by abnercoimbre": "abnercoimbre",
		"By Ada Lovelace": "Ada Lovelace",
		"  BY  Grace  ":   "Grace",
		"Bymaster":        "Bymaster",
		"by":              "by",
		"Ada":             "Ada",
		"":                "",
	} {
		if got := StripByline(in); got != want {
			t.Errorf("StripByline(%q) = %q, want %q", in, got, want)
		}
	}
}
