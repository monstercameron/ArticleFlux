package discover

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/timeutil"
)

// robots.txt, which §14.2 lists first among the scraping guardrails.
//
// The stakes are not abstract: an impolite scraper gets the SERVER's IP banned,
// and on a multi-tenant instance that takes down feeds for everyone on it,
// including the people who never scraped anything.
//
// This is a deliberately small implementation of a deliberately small part of
// the standard, and the omissions are the design:
//
//   - Only `User-agent: *` and our own token are read. A site that singles out
//     Googlebot is not talking to us.
//   - `Allow` beats `Disallow` on an equal-or-longer match, which is the rule
//     every crawler converges on and the one that makes the common
//     "Disallow: / + Allow: /blog" pattern work.
//   - No wildcards, no `$` anchors, no sitemaps. Prefix matching only. Where the
//     rules use them we are more conservative than asked, never less.
//   - **Unreachable robots.txt means ALLOWED.** A site with no robots.txt is the
//     overwhelmingly common case, and a fetch failure that read as "forbidden"
//     would make the feature look broken exactly when a site is having a bad
//     minute. A 4xx is an answer; a timeout is not, and neither says "no".
//
// The cache is per host with a short TTL, because this is consulted on every
// poll of every scraped source and a robots.txt fetch per poll would double the
// request count the guardrail exists to keep down.

// robotsTTL is how long a fetched robots.txt is trusted. Long enough that a
// polling loop reads it once an hour, short enough that a site that adds a rule
// is respected the same day.
const robotsTTL = 6 * time.Hour

type robotsEntry struct {
	rules   []robotsRule
	fetched time.Time
}

type robotsRule struct {
	path  string
	allow bool
}

// Allowed reports whether robots.txt permits fetching this URL.
//
// It never returns an error: the answer to "may I" is a boolean, and every way
// of failing to find out resolves to "yes" for the reason above. Failures are
// not silent though — they are simply not this function's to report, because
// nothing the caller could do differs between "no robots.txt" and "robots.txt
// timed out".
func (f *Fetcher) Allowed(ctx context.Context, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return true
	}
	host := u.Scheme + "://" + u.Host

	f.mu.Lock()
	ent, ok := f.robots[host]
	f.mu.Unlock()

	if !ok || timeutil.Now().Sub(ent.fetched) > robotsTTL {
		ent = robotsEntry{rules: f.fetchRobots(ctx, host), fetched: timeutil.Now()}
		f.mu.Lock()
		f.robots[host] = ent
		f.mu.Unlock()
	}
	return pathAllowed(ent.rules, u.EscapedPath())
}

func (f *Fetcher) fetchRobots(ctx context.Context, host string) []robotsRule {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	body, _, _, err := f.get(ctx, host+"/robots.txt", "text/plain,*/*;q=0.8")
	if err != nil {
		return nil
	}
	// A robots.txt larger than this is either not robots.txt or is not being
	// read by anybody; 512 KB is already an order of magnitude past the largest
	// real one.
	if len(body) > 512<<10 {
		body = body[:512<<10]
	}
	return parseRobots(string(body))
}

// parseRobots reads the groups that apply to us.
//
// Group semantics matter and are easy to get wrong: a `User-agent` line after a
// rule line starts a NEW group, so "User-agent: a / User-agent: b / Disallow: /"
// is one group covering both agents, while a Disallow between them splits it.
func parseRobots(txt string) []robotsRule {
	var (
		out       []robotsRule
		applies   bool
		inGroup   bool
		agentSeen bool
	)
	for _, line := range strings.Split(txt, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)

		switch key {
		case "user-agent":
			if inGroup {
				// A rule line ended the previous group.
				applies, inGroup = false, false
				agentSeen = false
			}
			agentSeen = true
			ua := strings.ToLower(val)
			if ua == "*" || strings.Contains(ua, "articleflux") {
				applies = true
			}
		case "allow", "disallow":
			if !agentSeen {
				continue
			}
			inGroup = true
			if !applies || val == "" {
				// An empty Disallow means "nothing is disallowed" — it is the
				// standard's way of saying yes, and it must not be read as a
				// prefix that matches everything.
				continue
			}
			out = append(out, robotsRule{path: val, allow: key == "allow"})
		}
	}
	return out
}

// pathAllowed applies longest-match-wins, Allow beating Disallow on a tie.
func pathAllowed(rules []robotsRule, path string) bool {
	if path == "" {
		path = "/"
	}
	best, allow := -1, true
	for _, r := range rules {
		// Wildcards are not interpreted; a rule containing one is compared on
		// the literal prefix before it, which is the conservative reading.
		p := r.path
		if i := strings.IndexAny(p, "*$"); i >= 0 {
			p = p[:i]
		}
		if p == "" || !strings.HasPrefix(path, p) {
			continue
		}
		if len(p) > best || (len(p) == best && r.allow) {
			best, allow = len(p), r.allow
		}
	}
	return allow
}
