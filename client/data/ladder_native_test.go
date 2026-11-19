//go:build !js

package data

import (
	"sort"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
)

// TestJEVRoutesMatchTheLadder holds the client's copy of the route names to
// llm.DeciderHosts, the egress boundary's own enumeration. Native only: the
// ladder package is not something the wasm test binary should link.
func TestJEVRoutesMatchTheLadder(t *testing.T) {
	var want []string
	for k := range llm.DeciderHosts {
		want = append(want, k)
	}
	got := append([]string(nil), JEVRoutes...)
	sort.Strings(want)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("client routes %v, ladder routes %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("client routes %v, ladder routes %v", got, want)
		}
	}
	for _, r := range JEVRoutes {
		if JEVRouteNeedsBaseURL(r) != (llm.DeciderHosts[r] == "") {
			t.Errorf("%s: needs a base URL = %v, but its fixed host is %q", r, JEVRouteNeedsBaseURL(r), llm.DeciderHosts[r])
		}
	}
}
