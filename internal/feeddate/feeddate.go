// Package feeddate parses the dates that actually appear in feeds, as opposed to
// the ones the specifications describe.
//
// RSS says RFC 822, Atom says RFC 3339, and neither is what arrives. Real feeds
// emit single-digit days, "GMT+0000", named zones no library resolves, missing
// zones entirely, and "-0000" (which RFC 2822 defines as "zone unknown" rather
// than UTC). A reader that rejects those loses items, and losing an item is
// worse than dating it approximately.
//
// The contract: Parse either returns a usable UTC time or reports failure, and
// the caller hands failure to timeutil.ClampPublished, which falls back to when
// we first saw the item.
package feeddate

import (
	"errors"
	"strings"
	"time"
)

// layouts are tried in order. Ordering is by specificity, not by popularity: a
// layout that parses more input goes later, so a precise match is never stolen
// by a loose one.
var layouts = []string{
	time.RFC3339Nano,                 // Atom, the good case
	time.RFC3339,                     //
	"2006-01-02T15:04:05.000Z07",     // some generators emit a one-digit zone
	time.RFC1123Z,                    // RSS done properly
	time.RFC1123,                     // ...with a named zone
	time.RFC822Z,                     // two-digit year, numeric zone
	time.RFC822,                      //
	time.RFC850,                      //
	time.ANSIC,                       //
	time.UnixDate,                    //
	time.RubyDate,                    //
	"Mon, 2 Jan 2006 15:04:05 -0700", // single-digit day, the most common deviation
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04:05",
	"Mon, 02 Jan 2006 15:04:05",    // no zone at all
	"Mon, 02 Jan 2006 15:04 -0700", // no seconds, numeric zone
	"Mon, 2 Jan 2006 15:04 -0700",  // ...and a single-digit day
	"Mon, 02 Jan 2006 15:04 MST",
	"Mon, 02 Jan 2006 15:04",     // no seconds, no zone
	"Mon, 2 Jan 2006 15:04",      //
	"Mon, 02 Jan 2006",           // date only
	"2 Jan 2006 15:04:05 -0700",  // no weekday
	"2 Jan 2006 15:04:05 MST",    //
	"02 Jan 2006 15:04:05 -0700", //
	"January 2, 2006 15:04:05",   // long month, seen in hand-rolled generators
	"January 2, 2006",            //
	"2006-01-02 15:04:05 -0700",  // SQL-ish, common in WordPress exports
	"2006-01-02 15:04:05Z07:00",  //
	"2006-01-02 15:04:05",        //
	"2006-01-02T15:04:05",        // ISO without a zone
	"2006-01-02",                 // date only
	"2006/01/02 15:04:05",        //
	"2006/01/02",                 //
	"01/02/2006 15:04:05",        // US order; ambiguous, so it is last
	"01/02/2006",                 //
}

// ErrUnparseable means none of the layouts matched. It is not exceptional — it
// is a routine outcome that the caller resolves with a fallback.
var ErrUnparseable = errors.New("feeddate: unrecognised date")

// Parse returns the instant in UTC.
//
// A date with no zone is read as UTC rather than as server-local. Server-local
// would make the same feed produce different timestamps depending on where the
// instance runs, which turns a deployment detail into a data difference.
func Parse(s string) (time.Time, error) {
	s = clean(s)
	if s == "" {
		return time.Time{}, ErrUnparseable
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return normalise(t), nil
		}
	}
	return time.Time{}, ErrUnparseable
}

// MustParse is for fixtures and tests.
func MustParse(s string) time.Time {
	t, err := Parse(s)
	if err != nil {
		panic("feeddate: " + s + ": " + err.Error())
	}
	return t
}

// zoneFixups rewrite forms that are common in feeds and unparseable by Go.
var zoneFixups = []struct{ from, to string }{
	// "GMT+0000" is a numeric offset wearing a name; Go reads the name and then
	// chokes on the digits.
	{"GMT+0000", "+0000"}, {"GMT-0000", "+0000"},
	{"UTC+0000", "+0000"}, {"UTC-0000", "+0000"},
	{" UT", " UTC"},
	// "-0000" means "zone unknown" in RFC 2822, not "UTC". Treating it as +0000
	// is the pragmatic reading and matches every other reader.
	{"-0000", "+0000"},
	// Zone names Go's parser accepts syntactically but cannot resolve to an
	// offset without a location database entry. Mapping them explicitly keeps the
	// offset rather than silently yielding UTC.
	{" EST", " -0500"}, {" EDT", " -0400"},
	{" CST", " -0600"}, {" CDT", " -0500"},
	{" MST", " -0700"}, {" MDT", " -0600"},
	{" PST", " -0800"}, {" PDT", " -0700"},
	{" BST", " +0100"}, {" CET", " +0100"}, {" CEST", " +0200"},
	{" IST", " +0530"}, {" JST", " +0900"}, {" AEST", " +1000"},
}

func clean(s string) string {
	s = strings.TrimSpace(s)
	// Collapse runs of whitespace: "Mon,  2 Jan" appears in the wild and no
	// layout matches a double space.
	s = strings.Join(strings.Fields(s), " ")
	// A trailing "(GMT)" style comment is legal RFC 2822 and matches no layout.
	if i := strings.Index(s, " ("); i > 0 && strings.HasSuffix(s, ")") {
		s = s[:i]
	}
	for _, f := range zoneFixups {
		s = strings.Replace(s, f.from, f.to, 1)
	}
	return strings.TrimSpace(s)
}

// normalise converts to UTC. A parse that produced no zone information lands in
// UTC already, which is the documented reading.
func normalise(t time.Time) time.Time { return t.UTC() }
