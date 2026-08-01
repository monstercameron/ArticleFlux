package i18n

// English copy for the My Feed settings tab (client/view/myfeedsettings.go):
// what the ranking believes about the reader, and the dial they turn on it.
//
// # The register this screen has to hold
//
// Every string here is describing a MACHINE'S GUESS about a person, to that
// person, and the guess is often wrong — that is why the screen exists. So the
// copy states observations and never conclusions: "named in 2 headlines you
// read" rather than "you follow this". The first is checkable and the second is
// a claim about somebody's interests that a bigram from a handset review has not
// earned.
//
// It also never apologises for the model or explains it away. A reader looking
// at "Pro Max" among the things they supposedly follow wants a button, not a
// paragraph about phrase extraction.
func init() {
	text(DefaultLocale, "myFeed", map[string]string{
		// --- the tab, and what it is for -------------------------------------
		//
		// The tab's own name is settings.tab.myfeed; this is the sentence under
		// it. It states what the screen is FOR in the reader's terms — the model
		// has an opinion, the opinion can be wrong, and this is where it is
		// argued with. "Personalisation" would name the mechanism and promise
		// nothing.
		"intro": "My Feed ranks unread articles from what you read, and every pick says why. " +
			"This is the whole picture it works from — change anything here that is not you.",

		// --- the summary -----------------------------------------------------
		"summaryGroup": "Right now",
		"factPicks":    "Picks on the page",
		"factTopics":   "Interests",
		"factThings":   "Things you follow",
		"factFeeds":    "Feeds competing",
		// Said plainly, and without a number: §18.4's cold start is not a
		// percentage anyone can act on. What matters is that the most personal
		// term is missing and that reading is what supplies it.
		"cold": "No pick has matched an interest yet. Interests need a few weeks of reading before " +
			"they mean anything — until then My Feed ranks on freshness, the feeds you open " +
			"most, and the things named below.",

		// --- the factor mix --------------------------------------------------
		"factorGroup": "What decided the page",
		"factorHint": "Every pick carries its reasons. This is how often each one was the " +
			"kind of judgement that put an article there.",
		"factorCount": "{n} of {total}",
		"factorEmpty": "Nothing is ranked yet, so there is nothing to explain.",

		// --- interests ---------------------------------------------------------
		"topicGroup": "Interests",
		"topicHint": "Clusters of what you have read, named from the words they have in " +
			"common. An interest whose words look like furniture rather than a subject is one " +
			"to turn down.",
		"topicEmpty": "No interests yet.",
		"topicMore":  "and {n} more",
		// Trend is a fact about the cluster, not a judgement of the reader.
		"trend.rising":  "rising",
		"trend.steady":  "steady",
		"trend.fading":  "fading",
		"trend.dormant": "dormant",

		// --- entities --------------------------------------------------------
		//
		// "Things you follow" is the label the ranking uses in its own reason
		// line ("about Android Auto, which you follow"), so the screen that
		// corrects it uses the same words. A reader matching a chip to a control
		// should not have to translate.
		"entityGroup": "Things you follow",
		"entityHint": "Names that keep coming up in what you read. These are found from " +
			"headlines, so a phrase can slip in that is not a subject at all.",
		"entityEmpty": "Nothing named yet.",
		// The evidence, stated as what was counted rather than as a score. A
		// weight of 37 from two mentions is the shape of a misread, and the row
		// shows both numbers so it is visible.
		"entityWeight": "reading weight {w}",
		"entityPhrase": "found in headlines",
		"entityModel":  "found by Smart+",
		// Used by both capped lists — the named things and the feeds — because
		// the sentence is about a display cap rather than about either subject.
		"capped": "Showing the strongest {n} of {total}.",

		// --- feeds -----------------------------------------------------------
		"feedGroup": "Feeds competing",
		"feedHint": "How closely you read each one, which is what decides how much its " +
			"articles are worth on the ranked page.",
		"feedEmpty":   "No feed has enough reading behind it yet.",
		"feedOpens":   "{opens} opened of {shown} shown",
		"feedOff":     "Not on My Feed",
		"feedPerFeed": "Whether a feed reaches My Feed at all is in that feed's own settings.",

		// --- the dial --------------------------------------------------------
		//
		// Four words, and the fourth is doing the real work. "Never" rather than
		// "Hide" or "Block": it is an instruction about the MODEL, not about the
		// articles — items keep arriving in their feeds, and only this judgement
		// stops being used.
		"level":        "How much this counts",
		"level.more":   "More",
		"level.normal": "Normal",
		"level.less":   "Less",
		"level.never":  "Never",
		"levelNeverHint": "Never means the model stops using this to choose articles. " +
			"Nothing is unsubscribed and nothing is hidden from your feeds.",

		// --- state -----------------------------------------------------------
		"loadError": "Could not read the profile: {err}",
		// A press that did not land, said as a note over a screen that is still
		// showing. It names the likely cause rather than only the failure: the
		// one thing that legitimately retires a cluster between a screen loading
		// and a button being pressed is a rebuild, and the screen has just
		// refreshed itself from it.
		"steerFailed": "That did not land: {err}. The list below has been refreshed — try again.",
		"rebuilding": "Saved. My Feed will be rebuilt from this in a moment — reopen it to " +
			"see the change.",
		"saved":   "Saved.",
		"refresh": "Refresh",

		// --- the scoring factors ---------------------------------------------
		//
		// Keyed by internal/rank's own term, and short noun phrases because they
		// head a row that already carries the count.
		//
		// These are the SAME judgements the reason line on a My Feed row states
		// in prose ("several feeds you follow carried this"), reduced to what
		// fits a label. Both halves ship on the wire for that reason — see
		// Item.rank_reason_terms in reader.proto.
		"factor.topic":            "Matched an interest",
		"factor.entity":           "Named something you follow",
		"factor.feed":             "From a feed you read closely",
		"factor.domain":           "Pointed at a site you open",
		"factor.fresh":            "Recent",
		"factor.corroboration":    "Carried by several feeds",
		"factor.manual":           "A rule of yours",
		"factor.volume":           "Demoted for volume",
		"factor.duplicate":        "Demoted as a near-duplicate",
		"factor.negative":         "Demoted as not an interest",
		"factor.skipped":          "Demoted for being scrolled past",
		"factor.external":         "Widely discussed at the source",
		"factor.deliberate":       "Something you starred or noted",
		"factor.concept_feedback": "Related to something you liked or disliked",
		"factor.smartplus":        "Moved up by Smart+ ranking",
	})

	// The two counted lines, as plurals. Both are rendered on every row of a
	// list that can run to sixty, which is exactly where an `if n == 1` at the
	// call site would be wrong in half the world's languages.
	plural(DefaultLocale, "myFeed", "topicMembers", map[PluralCategory]string{
		One:   "1 article",
		Other: "{count} articles",
	})
	// "named in headlines you read" and not "you follow this". The count is an
	// observation the reader can check against their own reading; the second is
	// a claim about them that two headlines have not earned.
	plural(DefaultLocale, "myFeed", "entityMentions", map[PluralCategory]string{
		One:   "named in 1 headline you read",
		Other: "named in {count} headlines you read",
	})
}
