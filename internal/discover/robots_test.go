package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRobotsGroups(t *testing.T) {
	const txt = `
# a comment
User-agent: Googlebot
Disallow: /

User-agent: *
Disallow: /private
Allow: /private/public
Disallow: /tmp
`
	rules := parseRobots(txt)

	for _, tc := range []struct {
		path string
		want bool
		why  string
	}{
		{"/", true, "nothing disallows the root for *"},
		{"/blog", true, "unlisted paths are allowed"},
		{"/private", false, "explicitly disallowed"},
		{"/private/thing", false, "prefix match"},
		{"/private/public", true, "Allow beats Disallow on the longer match"},
		{"/private/public/x", true, "…and on paths under it"},
		{"/tmp/x", false, "second rule in the same group still applies"},
	} {
		if got := pathAllowed(rules, tc.path); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v (%s)", tc.path, got, tc.want, tc.why)
		}
	}
}

// A rule aimed at somebody else is not a rule aimed at us.
func TestRobotsIgnoresOtherAgents(t *testing.T) {
	rules := parseRobots("User-agent: AhrefsBot\nDisallow: /\n")
	if !pathAllowed(rules, "/anything") {
		t.Error("a Disallow for AhrefsBot was applied to us")
	}
}

// An empty Disallow is the standard's way of saying yes, and reading it as a
// prefix that matches everything would block the whole site.
func TestRobotsEmptyDisallowAllowsEverything(t *testing.T) {
	rules := parseRobots("User-agent: *\nDisallow:\n")
	if !pathAllowed(rules, "/anything") {
		t.Error("an empty Disallow blocked the site")
	}
}

// An unreachable robots.txt means allowed: a site without one is the common
// case, and a fetch failure that read as "forbidden" would break the feature
// whenever a site had a bad minute.
func TestMissingRobotsAllows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if !local().Allowed(context.Background(), srv.URL+"/blog") {
		t.Error("a 404 robots.txt was read as a refusal")
	}
}

func TestRobotsRefusalIsHonoured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /blog\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	f := local()
	if f.Allowed(context.Background(), srv.URL+"/blog") {
		t.Error("a Disallow was ignored")
	}
	// …and the rest of the site is still fair game, which is what makes this a
	// rule rather than a switch.
	if !f.Allowed(context.Background(), srv.URL+"/news") {
		t.Error("a Disallow on /blog blocked /news")
	}
}
