package design

import (
	"regexp"
	"strconv"
	"testing"
)

// TestRailPitchMatchesTheStylesheet holds the rail's virtual window to its
// arithmetic. view/rail_window.go puts row n at n*RailRowPitch pixels, which is
// only true if the stylesheet makes every windowed row exactly that tall — and
// the stylesheet spells the number as a literal, so the two can drift apart
// without a compile error and the symptom is a spacer that is a few pixels off
// per row: rows that creep out of place as you scroll a long list.
func TestRailPitchMatchesTheStylesheet(t *testing.T) {
	sheet := sheetText(t)
	slot := regexp.MustCompile(`\.rail-list\[data-win\]\s*>\s*\.feed-slot\s*\{[^}]*height:\s*(\d+)px`).FindStringSubmatch(sheet)
	if slot == nil {
		t.Fatal("no height rule for a windowed run's rows")
	}
	if got, _ := strconv.Atoi(slot[1]); got != RailRowPitch {
		t.Errorf("a windowed row is %dpx in the stylesheet and RailRowPitch is %d", got, RailRowPitch)
	}
	row := regexp.MustCompile(`\.rail-list\[data-win\]\s*>\s*\.feed-slot\s*>\s*\.feed-row\s*\{[^}]*height:\s*(\d+)px`).FindStringSubmatch(sheet)
	if row == nil {
		t.Fatal("no height rule for the row inside a windowed slot")
	}
	if got, _ := strconv.Atoi(row[1]); got != RailRowPitch-1 {
		t.Errorf("the row inside a windowed slot is %dpx; it should be a pixel short of the %dpx pitch", got, RailRowPitch)
	}
}
