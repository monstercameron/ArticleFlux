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

// A URL that carries no host at all cannot be checked against anything, and
// must default to allowed for the same reason an unreachable robots.txt does.
func TestAllowedOnAHostlessURLDefaultsToTrue(t *testing.T) {
	f := local()
	if !f.Allowed(context.Background(), "not a url at all") {
		t.Error("a hostless address was read as disallowed")
	}
	if !f.Allowed(context.Background(), "/just/a/path") {
		t.Error("a path with no host was read as disallowed")
	}
}

// A robots.txt far past any real one is truncated rather than read in full —
// the same defensive cap MaxBodyBytes applies to pages, sized down for a
// document that is never legitimately this large.
func TestOversizedRobotsIsTruncatedNotRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		// A real disallow rule near the front, then padding well past the cap —
		// truncation must not corrupt the rule that was already parsed.
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /blog\n"))
		padding := make([]byte, 600<<10)
		for i := range padding {
			padding[i] = '#'
		}
		_, _ = w.Write(padding)
	}))
	defer srv.Close()

	f := local()
	if f.Allowed(context.Background(), srv.URL+"/blog") {
		t.Error("a Disallow rule ahead of the truncation point was lost")
	}
}

// A rule line before any User-agent line belongs to no group and must be
// ignored, not applied globally.
func TestRobotsRuleBeforeAnyUserAgentIsIgnored(t *testing.T) {
	rules := parseRobots("Disallow: /blog\nUser-agent: *\nAllow: /\n")
	if !pathAllowed(rules, "/blog") {
		t.Error("a Disallow line with no preceding User-agent was still applied")
	}
}

// pathAllowed on an empty path (a bare origin with nothing after the host)
// must be judged as "/", the same as an explicit root request.
func TestPathAllowedTreatsEmptyPathAsRoot(t *testing.T) {
	rules := parseRobots("User-agent: *\nDisallow: /\n")
	if pathAllowed(rules, "") {
		t.Error("an empty path was not judged against the root rule")
	}
}

// A wildcard in a rule is not interpreted; only the literal prefix before it
// is compared, which is the conservative reading §14.2 calls for.
func TestPathAllowedComparesOnlyTheLiteralPrefixBeforeAWildcard(t *testing.T) {
	rules := parseRobots("User-agent: *\nDisallow: /foo*bar\n")
	if pathAllowed(rules, "/foo/anything") == pathAllowed(rules, "/other") {
		t.Fatal("the wildcard rule matched nothing at all; the prefix comparison is broken")
	}
	if pathAllowed(rules, "/foo/anything") {
		t.Error("/foo/anything should match the literal prefix /foo before the wildcard")
	}
	if !pathAllowed(rules, "/other") {
		t.Error("/other must not match a rule scoped to /foo*")
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
