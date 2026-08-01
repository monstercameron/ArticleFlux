//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The My Feed settings tab: the interest model, shown and correctable
// (plan.md §18.2, §18.9).
//
// # Why a settings screen and not a control on the row
//
// Every ranked row already explains itself, and the obvious next move was a
// "not this" button beside the reason. It answers the wrong question. Looking at
// one row you can tell that the row is odd; you cannot tell that one phrase is
// deciding forty of them — and that is the failure this screen was built for. On
// a real database the strongest "thing you follow" was **Pro Max**, weight 37
// from two mentions, which is one handset review read closely and is not a
// subject anybody follows. Nothing on the ranked page was broken. There was
// simply nowhere to say the model had misread.
//
// So the unit here is the MODEL: topics, named things, feeds, and the mix of
// judgements behind the current page. Correcting one fixes every row it touches.
//
// # The screen shows evidence, then offers the dial
//
// Each row leads with what was OBSERVED — "named in 2 headlines you read",
// "3 articles", "12 opened of 340 shown" — and only then with the control. That
// ordering is the argument for the whole feature: a reader cannot judge a
// judgement they cannot see the basis for, and a dial beside an unexplained name
// is a dial nobody turns.
//
// It reuses the feed panel's `fs-*` vocabulary like every other settings tab,
// with one addition of its own: `mf-row`, which is a row whose evidence needs a
// second line.

type myFeedProps struct {
	profile *pb.GetInterestProfileResponse
	loading bool
	err     string
	// note is what just happened — "saved", "saved, rebuilding", or why a press
	// did not land. Held rather than derived, because the useful half of it (a
	// rebuild is coming) is only knowable from the response to the write.
	note string
	// noteBad marks the note as a failure rather than a confirmation.
	//
	// A failed WRITE is a note and a failed LOAD replaces the screen, and the
	// distinction is not cosmetic — it is a bug this screen shipped with. Routing
	// a rejected press into `err` blanked the entire profile: the reader pressed
	// a chip, the panel they were reading vanished, and all that was left was one
	// line of error text. The screen still holds a picture that is true; a press
	// that did not land does not stop it being true.
	noteBad bool
	// pending is the row a write is in flight for, keyed the way the actions
	// address rows: a topic id or an entity name. One at a time, because that is
	// how many buttons a person presses at once, and it lets the pressed row say
	// so without a per-row state map.
	pending string
	// confirmDelete is the one row armed for delete, as "kind:target" — see
	// reader.go's myFeedConfirmDelete. Empty when nothing is armed.
	confirmDelete string
	// confirmReset is the one category ("topics"/"entities"/"feeds") armed
	// for a reset. Empty when nothing is armed.
	confirmReset string
}

// The tab's actions, on the same delegated data-action path as everything else.
const (
	actMyFeedTopic   = "mf-topic"
	actMyFeedEntity  = "mf-entity"
	actMyFeedRefresh = "mf-refresh"
	// Deleting one row: arm asks, confirm does it, cancel backs out. Cancel
	// exists here (and not on the category editor's delete) because a My Feed
	// row sits in a scrolling list of many identical-looking controls, where
	// the category editor's delete is one button in a small, focused dialog —
	// a misarmed row here is easier to lose track of than a misarmed dialog.
	actMyFeedDeleteArm     = "mf-delete-arm"
	actMyFeedDeleteConfirm = "mf-delete-confirm"
	actMyFeedDeleteCancel  = "mf-delete-cancel"
	// Resetting a whole category back to default: same three-press shape.
	actMyFeedResetArm     = "mf-reset-arm"
	actMyFeedResetConfirm = "mf-reset-confirm"
	actMyFeedResetCancel  = "mf-reset-cancel"
)

// myFeedCategory names the three row kinds a delete/reset action can target —
// distinct from the topic/entity `mf-*` action names above because a reset
// addresses a CATEGORY, not a row, and the two vocabularies must not be
// interchangeable at the call site.
const (
	myFeedCatTopics   = "topics"
	myFeedCatEntities = "entities"
	myFeedCatFeeds    = "feeds"
)

// myFeedRowKind names what a single deletable row is, for
// armMyFeedDelete/deleteMyFeedRow's `kind` argument.
const (
	myFeedRowTopic  = "topic"
	myFeedRowEntity = "entity"
	myFeedRowFeed   = "feed"
)

// steerLevels is the dial, in the order it is drawn.
//
// More first and Never last, so the row reads as a scale rather than as a menu.
// Never is at the end because it is the one answer that is a correction rather
// than an adjustment, and because a destructive-feeling option under the
// reader's thumb at the start of a row is one that gets pressed by accident.
var steerLevels = []struct {
	level pb.SteerLevel
	key   string
}{
	{pb.SteerLevel_STEER_LEVEL_MORE, "more"},
	{pb.SteerLevel_STEER_LEVEL_NORMAL, "normal"},
	{pb.SteerLevel_STEER_LEVEL_LESS, "less"},
	{pb.SteerLevel_STEER_LEVEL_NEVER, "never"},
}

func settingsMyFeed(tr i18n.Runtime, p myFeedProps) []ui.Node {
	if p.err != "" {
		return []ui.Node{html.Div(html.Props{Class: "fs-error"}, html.Text(p.err))}
	}
	if p.loading && p.profile == nil {
		return settingsSkeleton()
	}
	pr := p.profile

	out := []ui.Node{
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("myFeed", "intro"))),
		ui.If(p.note != "", func() ui.Node {
			return html.Div(html.Props{
				Class: "set-note set-note-live", Role: "status",
				// The stylesheet colours it; the note itself SAYS which it is, so
				// the difference does not depend on seeing a rule change hue.
				Raw: map[string]any{"data-bad": strconv.FormatBool(p.noteBad)},
			}, html.Text(p.note))
		}),
	}
	out = append(out, myFeedSummary(tr, pr)...)
	out = append(out, myFeedFactors(tr, pr)...)
	out = append(out, myFeedTopics(tr, p)...)
	out = append(out, myFeedEntities(tr, p)...)
	out = append(out, myFeedFeeds(tr, p)...)
	out = append(out, html.Div(html.Props{Class: "set-actions"},
		glyphChip(actMyFeedRefresh, glyphRefresh, tr.T("myFeed", "refresh"), false)))
	return out
}

// --- the summary ----------------------------------------------------------------

func myFeedSummary(tr i18n.Runtime, pr *pb.GetInterestProfileResponse) []ui.Node {
	return []ui.Node{
		fsGroup(glyphMyFeed, tr.T("myFeed", "summaryGroup"), ""),
		setFact(tr.T("myFeed", "factPicks"), thousands(tr, int(pr.GetRankedCount()))),
		setFact(tr.T("myFeed", "factTopics"), thousands(tr, int(pr.GetTopicCount()))),
		setFact(tr.T("myFeed", "factThings"), thousands(tr, int(pr.GetEntityCount()))),
		// The TOTAL, not the length of the list below it. A summary line that
		// counted the rows it received would report the size of its own display
		// cap on any instance with more than twelve feeds.
		setFact(tr.T("myFeed", "factFeeds"), thousands(tr, int(pr.GetFeedCount()))),
		// The cold-start sentence, and only when it is true. A permanent note
		// saying the model is still learning is one nobody reads by the time it
		// matters.
		ui.If(pr.GetColdStart(), func() ui.Node {
			return html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("myFeed", "cold")))
		}),
	}
}

// --- the factor mix -------------------------------------------------------------

// myFeedFactors is the histogram of judgements behind the current page.
//
// A bar per term, widthed against the LARGEST factor rather than against the
// pick count. Against the count, a page where every pick is fresh and a third
// mention one entity draws one full bar and eleven slivers — true, and useless
// for the comparison the reader is making, which is between the factors.
func myFeedFactors(tr i18n.Runtime, pr *pb.GetInterestProfileResponse) []ui.Node {
	factors := pr.GetFactors()
	out := []ui.Node{
		fsGroup(glyphAction, tr.T("myFeed", "factorGroup"), tr.T("myFeed", "factorHint")),
	}
	if len(factors) == 0 {
		return append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("myFeed", "factorEmpty"))))
	}

	top := 1
	for _, f := range factors {
		if int(f.GetItems()) > top {
			top = int(f.GetItems())
		}
	}
	// What the factors were counted OVER, not how many picks exist. See
	// GetInterestProfileResponse.factor_base.
	total := int(pr.GetFactorBase())
	if total <= 0 {
		total = top
	}
	for _, f := range factors {
		pct := int(f.GetItems()) * 100 / top
		out = append(out, html.Div(html.Props{Class: "mf-factor", Key: "mf-f-" + f.GetTerm()},
			html.Span(html.Props{Class: "mf-factor-name"},
				html.Text(factorLabel(tr, f.GetTerm()))),
			html.Span(html.Props{Class: "mf-factor-count"},
				html.Text(tr.T("myFeed", "factorCount", i18n.Args{
					"n": thousands(tr, int(f.GetItems())), "total": thousands(tr, total),
				}))),
			// The bar is decoration for a number that is already written out, so
			// it is hidden from assistive technology rather than labelled twice.
			html.Span(html.Props{Class: "mf-bar", Aria: map[string]string{"hidden": "true"},
				Raw: map[string]any{"style": "--mf-fill:" + strconv.Itoa(pct) + "%"}}),
		))
	}
	return out
}

// factorLabel names a scoring term. The key is built from the term the server
// sent, so it is registered under the dynamic prefix `myFeed.factor.` — see
// client/i18n/keycoverage_test.go.
//
// An unknown term falls back to the term itself rather than to an empty row: a
// scorer that grows a new factor should show up on this screen as a word nobody
// has translated, not as a blank line with a number beside it.
func factorLabel(tr i18n.Runtime, term string) string {
	if term == "" {
		return ""
	}
	if s := tr.T("myFeed", "factor."+term); s != "" && !strings.HasPrefix(s, "myFeed.") {
		return s
	}
	return term
}

// --- topics ---------------------------------------------------------------------

func myFeedTopics(tr i18n.Runtime, p myFeedProps) []ui.Node {
	topics := p.profile.GetTopics()
	out := []ui.Node{
		fsGroup(glyphShared, tr.T("myFeed", "topicGroup"), tr.T("myFeed", "topicHint")),
	}
	if len(topics) > 0 {
		out = append(out, mfResetControl(myFeedCatTopics, p.confirmReset == myFeedCatTopics, tr))
	}
	if len(topics) == 0 {
		return append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("myFeed", "topicEmpty"))))
	}
	for _, t := range topics {
		out = append(out, mfRow(mfRowProps{
			key: "mf-t-" + t.GetId(),
			// The fingerprint, not the id: a derivation — including the one this
			// row's own press schedules — regenerates every topic id.
			target:      t.GetKey(),
			action:      actMyFeedTopic,
			kind:        myFeedRowTopic,
			name:        t.GetLabel(),
			evidence:    topicEvidence(tr, t),
			level:       t.GetLevel(),
			pending:     p.pending == t.GetId(),
			armedDelete: p.confirmDelete == myFeedRowTopic+":"+t.GetKey(),
		}, tr))
	}
	return out
}

// topicEvidence is what the cluster is, in the terms it was named from.
//
// The terms are the point of the line and the reason it is not just a count:
// "Max · Pro · 90s" is a label nobody can evaluate, and seeing the words it was
// built from is what lets a reader recognise a cluster the vectoriser assembled
// out of headline furniture.
func topicEvidence(tr i18n.Runtime, t *pb.InterestTopic) string {
	parts := []string{tr.T("myFeed", "topicMembers", i18n.Count(int(t.GetMemberCount())))}
	if trend := t.GetTrend(); trend != "" {
		if s := tr.T("myFeed", "trend."+trend); s != "" && !strings.HasPrefix(s, "myFeed.") {
			parts = append(parts, s)
		}
	}
	// Four terms, then a count. The whole list runs to eight and would wrap the
	// row on a narrow pane; four is enough to recognise a cluster by.
	const shown = 4
	terms := t.GetTerms()
	if len(terms) > 0 {
		head := terms
		if len(head) > shown {
			head = head[:shown]
		}
		line := strings.Join(head, " · ")
		if len(terms) > shown {
			line += " · " + tr.T("myFeed", "topicMore", i18n.Args{"n": len(terms) - shown})
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, " — ")
}

// --- things you follow ----------------------------------------------------------

func myFeedEntities(tr i18n.Runtime, p myFeedProps) []ui.Node {
	ents := p.profile.GetEntities()
	out := []ui.Node{
		fsGroup(glyphYours, tr.T("myFeed", "entityGroup"), tr.T("myFeed", "entityHint")),
	}
	if len(ents) > 0 {
		out = append(out, mfResetControl(myFeedCatEntities, p.confirmReset == myFeedCatEntities, tr))
	}
	if len(ents) == 0 {
		return append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("myFeed", "entityEmpty"))))
	}
	for _, e := range ents {
		out = append(out, mfRow(mfRowProps{
			key:         "mf-e-" + e.GetName(),
			target:      e.GetName(),
			action:      actMyFeedEntity,
			kind:        myFeedRowEntity,
			name:        e.GetLabel(),
			evidence:    entityEvidence(tr, e),
			level:       e.GetLevel(),
			pending:     p.pending == e.GetName(),
			armedDelete: p.confirmDelete == myFeedRowEntity+":"+e.GetName(),
		}, tr))
	}
	if total := int(p.profile.GetEntityCount()); total > len(ents) {
		out = append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("myFeed", "capped", i18n.Args{
				"n": thousands(tr, len(ents)), "total": thousands(tr, total),
			}))))
	}
	out = append(out, html.Div(html.Props{Class: "set-note"},
		html.Text(tr.T("myFeed", "levelNeverHint"))))
	return out
}

// entityEvidence states what was counted, not what it proves.
//
// Both numbers, always, and that pairing is the whole diagnostic: a reading
// weight of 37 built out of two headlines is one article read hard, and it is
// only recognisable as a misread when the mention count is sitting next to it.
func entityEvidence(tr i18n.Runtime, e *pb.InterestEntity) string {
	parts := []string{tr.T("myFeed", "entityMentions", i18n.Count(int(e.GetMentions())))}
	parts = append(parts, tr.T("myFeed", "entityWeight", i18n.Args{
		"w": strconv.FormatFloat(e.GetWeight(), 'f', 1, 64),
	}))
	// Which half of the system named it. The two have different failure modes,
	// and a reader correcting one is entitled to know which they are looking at.
	if e.GetKind() == "llm" {
		parts = append(parts, tr.T("myFeed", "entityModel"))
	} else {
		parts = append(parts, tr.T("myFeed", "entityPhrase"))
	}
	return strings.Join(parts, " — ")
}

// --- feeds ----------------------------------------------------------------------

// myFeedFeeds names the feed and its score, and now offers one write: taking
// a feed OUT of the ranked pool (in_megafeed=false), reached through the same
// delete-and-confirm control every other row here uses.
//
// It does not offer the dial's More/Normal/Less — that finer control lives in
// the feed's own settings beside its poll interval and its mute, and
// duplicating it here would give one setting two homes that can disagree.
// Delete is a coarser, different fact — "leave the ranked pool entirely" —
// which is why it gets its own control here rather than borrowing that one.
func myFeedFeeds(tr i18n.Runtime, p myFeedProps) []ui.Node {
	pr := p.profile
	feeds := pr.GetFeeds()
	out := []ui.Node{
		fsGroup(glyphFeeds, tr.T("myFeed", "feedGroup"), tr.T("myFeed", "feedHint")),
	}
	if len(feeds) > 0 {
		out = append(out, mfResetControl(myFeedCatFeeds, p.confirmReset == myFeedCatFeeds, tr))
	}
	if len(feeds) == 0 {
		return append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("myFeed", "feedEmpty"))))
	}
	for _, f := range feeds {
		pct := int(f.GetScore()*100 + 0.5)
		id := f.GetSourceId()
		out = append(out, html.Div(html.Props{
			Class: "mf-row", Key: "mf-s-" + id,
			Raw: map[string]any{"data-pending": strconv.FormatBool(p.pending == id)},
		},
			html.Div(html.Props{Class: "mf-main"},
				html.Span(html.Props{Class: "mf-name"}, html.Text(f.GetTitle())),
				html.Span(html.Props{Class: "mf-evidence"},
					html.Text(tr.T("myFeed", "feedOpens", i18n.Args{
						"opens": thousands(tr, int(f.GetOpens())),
						"shown": thousands(tr, int(f.GetImpressions())),
					}))),
			),
			html.Div(html.Props{Class: "mf-controls"},
				html.Div(html.Props{Class: "mf-control"},
					ui.If(!f.GetOnMyFeed(), func() ui.Node {
						return html.Span(html.Props{Class: "chip chip-static chip-off"},
							html.Text(tr.T("myFeed", "feedOff")))
					}),
					html.Span(html.Props{Class: "mf-score"}, html.Text(strconv.Itoa(pct)+"%")),
				),
				html.Div(html.Props{Class: "mf-control mf-control-delete"},
					mfDeleteControl(myFeedRowFeed, id, p.confirmDelete == myFeedRowFeed+":"+id, tr)...,
				),
			),
		))
	}
	if total := int(pr.GetFeedCount()); total > len(feeds) {
		out = append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("myFeed", "capped", i18n.Args{
				"n": thousands(tr, len(feeds)), "total": thousands(tr, total),
			}))))
	}
	return append(out, html.Div(html.Props{Class: "set-note"},
		html.Text(tr.T("myFeed", "feedPerFeed"))))
}

// --- the row and its dial -------------------------------------------------------

type mfRowProps struct {
	key    string
	target string
	action string
	// kind is myFeedRowTopic/myFeedRowEntity — which delete verb this row's
	// control reaches, and half of the "kind:target" key armMyFeedDelete
	// arms and deleteMyFeedRow reads back.
	kind        string
	name        string
	evidence    string
	level       pb.SteerLevel
	pending     bool
	armedDelete bool
}

func mfRow(p mfRowProps, tr i18n.Runtime) ui.Node {
	return html.Div(html.Props{
		Class: "mf-row", Key: p.key,
		Raw: map[string]any{
			// So the stylesheet can mark a struck-out row without the label
			// having to say "(off)" — and so a pending row can dim while its
			// write is in flight rather than looking like nothing happened.
			"data-level":   levelKey(p.level),
			"data-pending": strconv.FormatBool(p.pending),
		},
	},
		html.Div(html.Props{Class: "mf-main"},
			html.Span(html.Props{Class: "mf-name"}, html.Text(p.name)),
			html.Span(html.Props{Class: "mf-evidence"}, html.Text(p.evidence)),
		),
		// One flex wrapper around both controls, so .mf-row's space-between
		// still sees exactly two children (the evidence and everything that
		// acts on it) — a bare third child would open a gap of its own
		// between the dial and the delete chip instead of a small, deliberate
		// one.
		html.Div(html.Props{Class: "mf-controls"},
			html.Div(html.Props{Class: "mf-control", Role: "group",
				Aria: map[string]string{"label": tr.T("myFeed", "level")}},
				mfDial(p, tr)...,
			),
			html.Div(html.Props{Class: "mf-control mf-control-delete"},
				mfDeleteControl(p.kind, p.target, p.armedDelete, tr)...,
			),
		),
	)
}

// mfDeleteControl is the two-press remove: one chip that relabels itself and
// arms red on the first press (settings.go's signOutGroup — `fs-danger` at
// rest is an outline, `data-armed="true"` is what fills it, so the colour
// arrives WITH the consequence, never before it), then acts on the second.
//
// A small Cancel rides beside it once armed, which signOutGroup's own single
// button does not need. That screen is one control in a small dialog, easy
// to back out of just by leaving; a My Feed row sits in a scrolling list of
// near-identical rows, where an armed one the reader has scrolled past is
// easy to forget about. settingsTabTo still disarms every row on leaving the
// tab, as the same backstop signOutArmed gets.
func mfDeleteControl(kind, target string, armed bool, tr i18n.Runtime) []ui.Node {
	if target == "" {
		return nil
	}
	label, action := tr.T("myFeed", "rowDelete"), actMyFeedDeleteArm
	labelAria := tr.T("myFeed", "rowDeleteAria")
	class := "chip chip-mini"
	if armed {
		label, action = tr.T("myFeed", "rowDeleteConfirm"), actMyFeedDeleteConfirm
		labelAria = tr.T("myFeed", "rowDeleteConfirmAria")
		class = "chip chip-mini fs-danger"
	}
	out := []ui.Node{html.Button(html.Props{
		Class: class,
		Raw: map[string]any{
			"data-action": action, "data-for-item": target, "data-value": kind,
			"data-armed": strconv.FormatBool(armed),
		},
		Aria: map[string]string{"label": labelAria},
	}, html.Text(label))}
	if armed {
		out = append(out, html.Button(html.Props{
			Class: "chip chip-mini",
			Raw:   map[string]any{"data-action": actMyFeedDeleteCancel},
			Aria:  map[string]string{"label": tr.T("myFeed", "rowDeleteCancelAria")},
		}, html.Text(tr.T("myFeed", "rowDeleteCancel"))))
	}
	return out
}

// mfResetControl is the category-wide version of the same two-press shape,
// one per group rather than one per row. See resetMyFeedCategory for what it
// actually undoes in each of the three categories.
func mfResetControl(category string, armed bool, tr i18n.Runtime) ui.Node {
	label, action := tr.T("myFeed", "catReset"), actMyFeedResetArm
	labelAria := tr.T("myFeed", "catResetAria")
	class := "chip chip-mini"
	if armed {
		label, action = tr.T("myFeed", "catResetConfirm"), actMyFeedResetConfirm
		labelAria = tr.T("myFeed", "catResetConfirmAria")
		class = "chip chip-mini fs-danger"
	}
	kids := []ui.Node{html.Button(html.Props{
		Class: class,
		Raw: map[string]any{
			"data-action": action, "data-value": category, "data-armed": strconv.FormatBool(armed),
		},
		Aria: map[string]string{"label": labelAria},
	}, html.Text(label))}
	if armed {
		kids = append(kids,
			html.Button(html.Props{
				Class: "chip chip-mini",
				Raw:   map[string]any{"data-action": actMyFeedResetCancel},
				Aria:  map[string]string{"label": tr.T("myFeed", "catResetCancelAria")},
			}, html.Text(tr.T("myFeed", "rowDeleteCancel"))),
			// A live region, the same reason signOutGroup's warning is one: arming
			// changes what the NEXT press does, across every row in the category,
			// and that has to reach a reader who cannot see the button turn red.
			html.Div(html.Props{Class: "set-note set-note-live", Role: "alert"},
				html.Text(tr.T("myFeed", "catResetWarn"))),
		)
	}
	return html.Div(html.Props{Class: "set-actions mf-reset"}, kids...)
}

func mfDial(p mfRowProps, tr i18n.Runtime) []ui.Node {
	out := make([]ui.Node, 0, len(steerLevels))
	for _, l := range steerLevels {
		out = append(out, html.Button(html.Props{
			Class: "chip chip-mini",
			Key:   p.key + "-" + l.key,
			Raw: map[string]any{
				"data-action":   p.action,
				"data-for-item": p.target,
				"data-value":    l.key,
			},
			Aria: map[string]string{"pressed": strconv.FormatBool(l.level == effectiveLevel(p.level))},
		}, html.Text(tr.T("myFeed", "level."+l.key))))
	}
	return out
}

// effectiveLevel resolves UNSPECIFIED to Normal.
//
// A server that has not been told anything about a row means the dial is where
// it started, and an unspecified value would draw four unpressed chips — a
// segmented control with no current position, which reads as broken rather than
// as neutral.
func effectiveLevel(l pb.SteerLevel) pb.SteerLevel {
	if l == pb.SteerLevel_STEER_LEVEL_UNSPECIFIED {
		return pb.SteerLevel_STEER_LEVEL_NORMAL
	}
	return l
}

// levelKey is the stylesheet's handle on a row's position, and the value the
// click handler sends back.
func levelKey(l pb.SteerLevel) string {
	switch effectiveLevel(l) {
	case pb.SteerLevel_STEER_LEVEL_MORE:
		return "more"
	case pb.SteerLevel_STEER_LEVEL_LESS:
		return "less"
	case pb.SteerLevel_STEER_LEVEL_NEVER:
		return "never"
	}
	return "normal"
}

// steerLevelOf is the inverse, for the value a click carries.
func steerLevelOf(key string) pb.SteerLevel {
	for _, l := range steerLevels {
		if l.key == key {
			return l.level
		}
	}
	return pb.SteerLevel_STEER_LEVEL_UNSPECIFIED
}
