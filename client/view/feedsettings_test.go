//go:build js && wasm

package view

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// How a settings row's words reach its control.
//
// Every row on the settings surfaces draws a label and a hint beside a control,
// and for years that was all it was — two neighbours on screen with nothing
// joining them for anyone not reading the layout. These pin the join: a row is
// a group named and described by what it shows, a field is labelled by a real
// <label>, and every id one element points at is an id some element has.
//
// The last of those is the one a later edit breaks without noticing. An
// aria-labelledby naming a missing id is not an error anywhere — the browser
// quietly computes an empty name — so the only place it can fail loudly is here.

var (
	idAttrRe   = regexp.MustCompile(` id="([^"]+)"`)
	idRefRe    = regexp.MustCompile(` (?:aria-labelledby|aria-describedby|for)="([^"]+)"`)
	inputTagRe = regexp.MustCompile(`<(?:input|select)\b[^>]*>`)
)

// assertIDRefsResolve fails on any reference to an id the document does not
// contain, and on any id that appears twice — a <label for> or a labelledby
// pointing at a duplicate reaches whichever one the browser finds first.
func assertIDRefsResolve(t *testing.T, what, out string) {
	t.Helper()
	ids := map[string]int{}
	for _, m := range idAttrRe.FindAllStringSubmatch(out, -1) {
		ids[m[1]]++
	}
	for id, n := range ids {
		if n > 1 {
			t.Errorf("%s: id %q appears %d times", what, id, n)
		}
	}
	for _, m := range idRefRe.FindAllStringSubmatch(out, -1) {
		for _, ref := range strings.Fields(m[1]) {
			if ids[ref] == 0 {
				t.Errorf("%s: %q points at id %q, which nothing has:\n%s", what, m[0], ref, out)
			}
		}
	}
}

func TestARowIsAGroupNamedAndDescribedByWhatItShows(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return legacyFsRow("Mute", "Keep polling it, keep it out of the way.",
			html.Button(html.Props{}, html.Text("a week")))
	})
	id := rowID("Mute", "Keep polling it, keep it out of the way.")
	for _, want := range []string{
		`role="group"`,
		`aria-labelledby="` + id + `-label"`,
		`aria-describedby="` + id + `-hint"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fsRow lacks %s:\n%s", want, out)
		}
	}
	assertIDRefsResolve(t, "fsRow", out)
}

// A row with no hint must not describe itself by an element it did not draw.
func TestARowWithNoHintDescribesNothing(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return legacyFsRow("Feed URL", "", html.Span(html.Props{}, html.Text("https://example.com/feed")))
	})
	if strings.Contains(out, "aria-describedby") {
		t.Errorf("a hintless row still carries a description:\n%s", out)
	}
	assertIDRefsResolve(t, "hintless fsRow", out)
}

// The id is derived, so it has to be stable across renders and different for
// different rows — two rows sharing one would label each other.
func TestRowIDsAreStableAndDistinct(t *testing.T) {
	if first, again := rowID("Mute", "x"), rowID("Mute", "x"); first != again {
		t.Errorf("rowID is not deterministic: %q then %q", first, again)
	}
	if rowID("Mute", "x") == rowID("Mute", "y") || rowID("Mute", "") == rowID("Cache", "") {
		t.Error("rowID gives two different rows the same id")
	}
	if strings.ContainsAny(rowID("Odds & ends", "a b"), " &") {
		t.Errorf("rowID %q is not a usable id", rowID("Odds & ends", "a b"))
	}
}

func feedSettingsHTML(t *testing.T) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		return feedSettings(tr, feedSettingsProps{open: true, s: &pb.FeedSettings{
			SourceId: "s1", ResolvedTitle: "Example", FeedUrl: "https://example.com/feed",
			SiteUrl: "https://example.com", SubscriberCount: 3, ConsecutiveFailures: 2,
		}, tags: []string{"rust"}})
	})
}

// TestTheRenameFieldIsNamedByItsVisibleLabel is "label in name". The field used
// to be called "Your name for this feed" beside a label reading "Name", so
// somebody driving the page by voice — who says the word they can see — could
// not reach it.
func TestTheRenameFieldIsNamedByItsVisibleLabel(t *testing.T) {
	out := feedSettingsHTML(t)
	if !strings.Contains(out, `for="`+fieldFeedTitle+`"`) {
		t.Errorf("the rename field has no <label for>:\n%s", out)
	}
	for _, tag := range inputTagRe.FindAllString(out, -1) {
		if strings.Contains(tag, `data-role="feed-title"`) {
			if !strings.Contains(tag, `id="`+fieldFeedTitle+`"`) {
				t.Errorf("the rename field does not carry the id its label points at: %s", tag)
			}
			if strings.Contains(tag, "aria-label=") {
				t.Errorf("the rename field still overrides its visible label: %s", tag)
			}
		}
	}
	assertIDRefsResolve(t, "feed settings", out)
}

// TestHealthIsADescriptionList: the name/value lines are dt/dd pairs, and a dt
// is only meaningful inside a dl.
func TestHealthIsADescriptionList(t *testing.T) {
	out := feedSettingsHTML(t)
	if !strings.Contains(out, `<dl class="fs-facts">`) || !strings.Contains(out, `<dt class="fs-fact-name">`) {
		t.Fatalf("the health facts are not a description list:\n%s", out)
	}
	assertTermsInsideLists(t, "feed settings", out)
}

// assertTermsInsideLists fails on a dt or dd that is not inside a dl — which is
// what calling setFact without setFacts around it would produce.
func assertTermsInsideLists(t *testing.T, what, out string) {
	t.Helper()
	depth := 0
	for i := 0; i < len(out); i++ {
		switch {
		case strings.HasPrefix(out[i:], "<dl"):
			depth++
		case strings.HasPrefix(out[i:], "</dl>"):
			depth--
		case strings.HasPrefix(out[i:], "<dt") || strings.HasPrefix(out[i:], "<dd"):
			if depth == 0 {
				t.Errorf("%s: a %s outside any <dl>:\n%s", what, out[i:i+3], out)
				return
			}
		}
	}
}

func TestEveryDialogsReferencesResolve(t *testing.T) {
	cases := map[string]func(tr i18n.Runtime) ui.Node{
		"add feed": func(tr i18n.Runtime) ui.Node {
			return addFeedDialog(tr, addFeedProps{open: true, newOpen: true,
				folders: []*pb.Folder{{Id: "f1", Name: "News"}}})
		},
		"category": func(tr i18n.Runtime) ui.Node {
			return categoryDialog(tr, categoryProps{open: true, id: "f1", name: "News", feeds: 2, confirm: true})
		},
		"tag settings": func(tr i18n.Runtime) ui.Node {
			return tagSettings(tr, tagSettingsProps{open: true, t: &pb.Tag{Id: "t1", Name: "rust"},
				feeds: []string{"Example"}})
		},
		"credential confirm": func(tr i18n.Runtime) ui.Node {
			return credConfirmDialog(tr, pendingPassword, "", "cam")
		},
	}
	for name, build := range cases {
		assertIDRefsResolve(t, name, renderView(t, build))
	}
}

// TestAddFeedLabelsItsFieldsAndNamesItsChoice: the two text fields are
// labelled by their eyebrows, and the folder chips — a choice, not a field —
// are a group named by theirs.
func TestAddFeedLabelsItsFieldsAndNamesItsChoice(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return addFeedDialog(tr, addFeedProps{open: true})
	})
	for _, want := range []string{
		`for="` + fieldAddURL + `"`, `id="` + fieldAddURL + `"`,
		`for="` + fieldAddTitle + `"`, `id="` + fieldAddTitle + `"`,
		`enterkeyhint="go"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("add feed lacks %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, `class="af-field" role="group"`) {
		t.Errorf("the folder chips are not a named group:\n%s", out)
	}
}

// TestTheLampIsNamedByItsWords: the long explanation is the lamp's title (its
// description), not a forty-word accessible name for a two-word button.
func TestTheLampIsNamedByItsWords(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return smartLamp(tr, true, actAddSmart, "Smart+ follow", "Smart+ follow — the long version")
	})
	if strings.Contains(out, "aria-label=") {
		t.Errorf("the lamp still overrides its visible words:\n%s", out)
	}
	if !strings.Contains(out, `title="Smart+ follow — the long version"`) {
		t.Errorf("the lamp lost its explanation:\n%s", out)
	}
}

// TestArmingTheFolderDeleteIsAnnounced: the second press deletes, and the
// sentence saying so has to reach somebody who cannot see the button change.
func TestArmingTheFolderDeleteIsAnnounced(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return categoryDialog(tr, categoryProps{open: true, id: "f1", name: "News", confirm: true})
	})
	if !strings.Contains(out, `class="af-warn" role="alert"`) {
		t.Errorf("the armed delete's warning is not a live region:\n%s", out)
	}
}

// --- the Settings surfaces, which are built from the same rows -------------------

func settingsBodyHTML(t *testing.T, build func(tr i18n.Runtime) []ui.Node) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		return html.Div(html.Props{}, build(tr)...)
	})
}

// TestEverySettingsTabsReferencesResolve renders the tabs this pass touched and
// checks the one thing nothing else will: that every label, description and
// <label for> lands on an element that exists, and exactly one.
func TestEverySettingsTabsReferencesResolve(t *testing.T) {
	stats := &pb.GetServerStatsResponse{Version: "1.0", Methods: []*pb.MethodStat{
		{Method: "GetFeeds", Calls: 10, P50Ms: 3, P95Ms: 12, MaxMs: 40, Errors: 2},
	}}
	cfg := &pb.GetSmartConfigResponse{Configured: true, KeyHint: "4f2a", DefaultModel: "gpt-x"}
	cases := map[string]func(tr i18n.Runtime) []ui.Node{
		"reading":   func(tr i18n.Runtime) []ui.Node { return settingsReading(tr, settingsProps{}) },
		"listening": func(tr i18n.Runtime) []ui.Node { return settingsListening(tr, settingsProps{speakPodcast: true}) },
		"feeds":     func(tr i18n.Runtime) []ui.Node { return settingsFeeds(tr, settingsProps{}) },
		"account": func(tr i18n.Runtime) []ui.Node {
			return settingsAccount(tr, settingsProps{whoami: "cam", reconnects: 1,
				password: passwordProps{username: "cam"}})
		},
		"server": func(tr i18n.Runtime) []ui.Node { return settingsServer(tr, settingsProps{stats: stats}) },
		"speed":  func(tr i18n.Runtime) []ui.Node { return settingsSpeed(tr, settingsProps{stats: stats}) },
		"smart": func(tr i18n.Runtime) []ui.Node {
			return settingsSmart(tr, smartProps{cfg: cfg, models: []string{"gpt-x"}})
		},
		"smart, typed model": func(tr i18n.Runtime) []ui.Node {
			return settingsSmart(tr, smartProps{cfg: cfg})
		},
		"classify": func(tr i18n.Runtime) []ui.Node { return settingsClassify(tr, classifySettingsProps{}) },
		"podcast": func(tr i18n.Runtime) []ui.Node {
			return settingsPodcast(tr, podcastProps{podcast: true, needs: []slidePrereq{
				{Key: prereqSmartVoice, Fixable: true}, {Key: prereqServerKey, On: true},
			}})
		},
	}
	for name, build := range cases {
		out := settingsBodyHTML(t, build)
		assertIDRefsResolve(t, name, out)
		assertTermsInsideLists(t, name, out)
	}
}

// TestAPickerInARowIsNotASecondGroup: the row is the group now, named by the
// same words, so the picker inside must not name itself again — two nested
// groups with one name is the name read twice, and an e2e locator asking for
// "the group called Opening sting and music" would find two.
func TestAPickerInARowIsNotASecondGroup(t *testing.T) {
	out := settingsBodyHTML(t, func(tr i18n.Runtime) []ui.Node {
		return settingsListening(tr, settingsProps{speakPodcast: true})
	})
	if n := strings.Count(out, `aria-label="Opening sting and music"`); n != 0 {
		t.Errorf("the bed picker still names itself (%d):\n%s", n, out)
	}
	if !strings.Contains(out, `>Opening sting and music</span>`) {
		t.Fatalf("the bed row lost its label:\n%s", out)
	}
}

// TestTheLatencyTableIsATable: a call per row and a figure per column, said so,
// so a reader moving down the p95 column hears which call each figure is for.
func TestTheLatencyTableIsATable(t *testing.T) {
	out := settingsBodyHTML(t, func(tr i18n.Runtime) []ui.Node {
		return settingsSpeed(tr, settingsProps{stats: &pb.GetServerStatsResponse{
			Methods: []*pb.MethodStat{{Method: "GetFeeds"}, {Method: "ListItems"}},
		}})
	})
	for want, n := range map[string]int{
		`role="table"`: 1, `role="row"`: 3, `role="columnheader"`: 5,
		`role="rowheader"`: 2, `role="cell"`: 8,
	} {
		if got := strings.Count(out, want); got != n {
			t.Errorf("%s: %d, want %d:\n%s", want, got, n, out)
		}
	}
}

// TestTheAPIKeyIsKeptFromAutofill: a key is a credential nobody will think to
// revoke from a password manager, and a phone keyboard that capitalises its
// first character has typed a key that does not work.
func TestTheAPIKeyIsKeptFromAutofill(t *testing.T) {
	out := settingsBodyHTML(t, func(tr i18n.Runtime) []ui.Node {
		return smartKeySection(tr, smartProps{cfg: &pb.GetSmartConfigResponse{}})
	})
	var key string
	for _, tag := range inputTagRe.FindAllString(out, -1) {
		if strings.Contains(tag, `data-role="smart-key"`) {
			key = tag
		}
	}
	if key == "" {
		t.Fatalf("no key field:\n%s", out)
	}
	for _, want := range []string{`autocomplete="off"`, `spellcheck="false"`,
		`autocapitalize="off"`, `data-1p-ignore`, `data-lpignore="true"`, `id="` + fieldSmartKey + `"`} {
		if !strings.Contains(key, want) {
			t.Errorf("the key field lacks %s: %s", want, key)
		}
	}
}

// TestEveryMyFeedDialSaysWhichRow: forty identically named groups is a list in
// which a listener cannot tell which dial they are on.
func TestEveryMyFeedDialSaysWhichRow(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return mfRow(mfRowProps{key: "k", target: "rust", action: "a", kind: myFeedRowTopic,
			name: "rust"}, tr)
	})
	if !strings.Contains(out, `aria-label="How much “rust” counts"`) {
		t.Errorf("the dial's group does not name its row:\n%s", out)
	}
	// Label in name: the delete chip's accessible name starts with its words.
	if !strings.Contains(out, `aria-label="Remove “rust” from My Feed"`) {
		t.Errorf("the delete chip's name does not lead with what it says:\n%s", out)
	}
}

// TestActionsAreNotToggles: Fetch now, Mark all read and Rename do something
// once. Any aria-pressed, "false" included, announces them as a switch that is
// off — which a reader might reasonably try to turn on.
func TestActionsAreNotToggles(t *testing.T) {
	out := feedSettingsHTML(t)
	buttonRe := regexp.MustCompile(`<button\b[^>]*>`)
	for _, tag := range buttonRe.FindAllString(out, -1) {
		for _, action := range []string{"fs-rename", "fs-refresh", "fs-markall"} {
			if strings.Contains(tag, `data-action="`+action+`"`) && strings.Contains(tag, "aria-pressed") {
				t.Errorf("%s is announced as a toggle: %s", action, tag)
			}
		}
	}
	feeds := settingsBodyHTML(t, func(tr i18n.Runtime) []ui.Node { return settingsFeeds(tr, settingsProps{}) })
	if strings.Contains(feeds, "aria-pressed") {
		t.Errorf("the Feeds tab's bulk actions are announced as toggles:\n%s", feeds)
	}
}

// backlogButtons returns the opening tag of every button in the "Clear the
// backlog" row, keyed by its data-value.
func backlogButtons(t *testing.T, days int32) map[string]string {
	t.Helper()
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return feedSettings(tr, feedSettingsProps{open: true, s: &pb.FeedSettings{
			SourceId: "s1", ResolvedTitle: "Example", FeedUrl: "https://example.com/feed",
			ClearBacklogDays: days,
		}})
	})
	buttonRe := regexp.MustCompile(`<button\b[^>]*>`)
	valueRe := regexp.MustCompile(`data-value="([^"]*)"`)
	got := map[string]string{}
	for _, tag := range buttonRe.FindAllString(out, -1) {
		if !strings.Contains(tag, `data-action="fs-backlog"`) {
			continue
		}
		if m := valueRe.FindStringSubmatch(tag); m != nil {
			got[m[1]] = tag
		}
	}
	return got
}

// The row offers exactly the five windows, and the one in force is the one
// that is pressed — a reader who cannot tell which window is on cannot tell
// whether their pile is being cleared.
func TestTheBacklogRowOffersTheWindowsAndPressesTheCurrentOne(t *testing.T) {
	for _, days := range []int32{0, 1, 3, 7, 30} {
		got := backlogButtons(t, days)
		if len(got) != 5 {
			t.Fatalf("the row has %d choices, want 5: %v", len(got), got)
		}
		for v, tag := range got {
			pressed := strings.Contains(tag, `aria-pressed="true"`)
			if want := v == strconv.Itoa(int(days)); pressed != want {
				t.Errorf("window %s with %d in force: pressed = %v, want %v\n%s", v, days, pressed, want, tag)
			}
		}
	}
}

// The one-press "clear it now" already exists as Mark all read, so the row
// points at it instead of growing a second button that does the same thing.
func TestTheBacklogRowPointsAtMarkAllReadAndAddsNoSecondButton(t *testing.T) {
	out := feedSettingsHTML(t)
	if n := strings.Count(out, `data-action="fs-markall"`); n != 1 {
		t.Errorf("%d Mark all read buttons, want exactly 1", n)
	}
	for _, want := range []string{"Clear the backlog", "Mark all read, below"} {
		if !strings.Contains(out, want) {
			t.Errorf("the panel does not say %q", want)
		}
	}
}
