//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The fold control is an icon, so its name is all a screen reader gets — and
// the name has to be what pressing it will DO, which is the opposite thing in
// each state. A control that said "Fold the feeds away" while they were folded
// would be announcing the state as an instruction.
func TestRailFoldButtonNamesWhatPressingItWillDo(t *testing.T) {
	for _, c := range []struct {
		folded        bool
		pressed, name string
	}{
		{false, `aria-pressed="false"`, "Fold the feeds away"},
		{true, `aria-pressed="true"`, "Keep the feeds open"},
	} {
		out := renderView(t, func(tr i18n.Runtime) ui.Node { return railFoldButton(tr, c.folded) })
		btn := buttonBlock(t, out, `data-action="`+actRailFold+`"`)
		if !strings.Contains(btn, c.pressed) {
			t.Errorf("folded=%v: want %s in %s", c.folded, c.pressed, btn)
		}
		if !strings.Contains(btn, `aria-label="`+c.name+`"`) {
			t.Errorf("folded=%v: the button is not named %q: %s", c.folded, c.name, btn)
		}
		// The chevron is decoration; read aloud it is "left-pointing double
		// angle quotation mark".
		if !strings.Contains(btn, `<span aria-hidden="true">`) {
			t.Errorf("folded=%v: the glyph is exposed to assistive tech: %s", c.folded, btn)
		}
	}
}

// The stylesheet selects on these two words (design/railfold.go). If the view
// wrote anything else the rail would simply never fold, with nothing failing.
func TestRailDataIsWhatTheSheetSelectsOn(t *testing.T) {
	if railData(true) != "folded" || railData(false) != "open" {
		t.Errorf("railData = %q / %q; design/railfold.go selects [data-rail='folded']",
			railData(true), railData(false))
	}
}

// Previous and Next step from the article whose bar was pressed. With two
// articles on screen, "current" is the upper one; a press on the lower one's
// Next must not skip from the upper.
func TestAnArticlesOwnStepIsMeasuredFromThatArticle(t *testing.T) {
	a, b, c := &pb.Item{Id: "a"}, &pb.Item{Id: "b"}, &pb.Item{Id: "c"}
	list := []*pb.Item{a, b, c}
	id := func(it *pb.Item) string {
		if it == nil {
			return "<none>"
		}
		return it.GetId()
	}
	for _, tc := range []struct {
		name    string
		current *pb.Item
		from    string
		delta   int
		want    string
	}{
		{"next, from the lower of two on screen", a, "b", 1, "c"},
		{"previous, from the lower of two on screen", a, "b", -1, "a"},
		{"next at the end of the list goes nowhere", a, "c", 1, "<none>"},
		{"previous at the start goes nowhere", b, "a", -1, "<none>"},
		{"an article the list no longer holds steps from the current one", b, "gone", 1, "c"},
		{"no id at all is the keyboard's question", b, "", -1, "a"},
	} {
		if got := id(navItemFrom(list, tc.current, tc.from, tc.delta)); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Each article's bar ends with its own Previous and Next, carrying that
// article's id — which is what lets the click handler answer the question
// above at all.
func TestEveryArticleBarEndsWithItsOwnPreviousAndNext(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return articleStep(tr, "art-7")
	})
	for _, want := range []string{
		`data-action="` + actNavPrev + `"`, `data-action="` + actNavNext + `"`,
		`aria-label="Previous article"`, `aria-label="Next article"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the step is missing %s: %s", want, out)
		}
	}
	if n := strings.Count(out, `data-for-item="art-7"`); n != 2 {
		t.Errorf("%d of the two step buttons carry their article's id: %s", n, out)
	}
}
