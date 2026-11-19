//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The first article has nothing before it and the last nothing after, and the
// bar says so with the real attribute rather than a press that does nothing.
func TestArticleStepDisablesTheEnds(t *testing.T) {
	render := func(first, last bool) string {
		return renderView(t, func(tr i18n.Runtime) ui.Node { return articleStep(tr, "a", first, last) })
	}
	middle, atFirst, atLast := render(false, false), render(true, false), render(false, true)
	if strings.Contains(middle, "disabled") {
		t.Errorf("a middle article's step has a disabled button: %s", middle)
	}
	if n := strings.Count(atFirst, "disabled"); n != 1 || strings.Index(atFirst, "disabled") > strings.Index(atFirst, actNavNext) {
		t.Errorf("the first article should disable Previous only: %s", atFirst)
	}
	if n := strings.Count(atLast, "disabled"); n != 1 || strings.Index(atLast, "disabled") < strings.Index(atLast, actNavNext) {
		t.Errorf("the last article should disable Next only: %s", atLast)
	}
}

// The list's last article is only its end once the cursor has run out: the last
// LOADED one has more after it.
func TestListEnds(t *testing.T) {
	list := []*pb.Item{{Id: "a"}, {Id: "b"}}
	if got := listFirstID(list); got != "a" {
		t.Errorf("first = %q", got)
	}
	if got := listLastID(list, ""); got != "b" {
		t.Errorf("last = %q", got)
	}
	if got := listLastID(list, "more"); got != "" {
		t.Errorf("last with a cursor = %q, want none", got)
	}
	if listFirstID(nil) != "" || listLastID(nil, "") != "" {
		t.Errorf("an empty list has no ends")
	}
}
