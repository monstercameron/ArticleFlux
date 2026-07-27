package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// music is the Music category (plan.md §27.3d #18).
//
// `ep` is left out entirely rather than guarded — it collides with too many
// unrelated acronyms (European Parliament chief among them) for a three-
// letter guard list to be worth writing, so `extended play` carries the
// weight instead.
func music() classify.Label {
	return classify.Label{
		Slug: "music",
		Name: "Music",
		Terms: []classify.Term{
			{Text: "billboard chart", Weight: 2.4},
			{Text: "grammy award", Weight: 2.4},
			{Text: "grammy nomination", Weight: 2.2},
			{Text: "debut album", Weight: 2.0},
			{Text: "sophomore album", Weight: 2.0},
			{Text: "platinum record", Weight: 2.0},
			{Text: "sold-out show", Weight: 2.0},
			{Text: "arena tour", Weight: 2.0},
			{Text: "album release", Weight: 2.0},
			{Text: "world tour", Weight: 2.0},
			{Text: "music festival", Weight: 2.0},
			{Text: "vinyl record", Weight: 2.0},
			{Text: "tour dates", Weight: 2.0},
			{Text: "tour cancellation", Weight: 2.0},
			{Text: "chart debut", Weight: 1.8},
			{Text: "gold record", Weight: 1.8},
			{Text: "studio album", Weight: 1.8},
			{Text: "live album", Weight: 1.8},
			{Text: "b-side", Weight: 1.8},
			{Text: "setlist", Weight: 1.8},
			{Text: "headline slot", Weight: 1.8},
			{Text: "record label", Weight: 1.8},
			{Text: "music streaming", Weight: 1.8},
			{Text: "chart-topping", Weight: 1.8},
			{Text: "indie label", Weight: 1.6},
			{Text: "major label", Weight: 1.6},
			{Text: "single release", Weight: 1.8},
			{Text: "music producer", Weight: 1.6},
			{Text: "singer-songwriter", Weight: 1.6},
			{Text: "opening act", Weight: 1.6},
			{Text: "featured artist", Weight: 1.6},
			{Text: "extended play", Weight: 1.6},
			{Text: "philharmonic", Weight: 1.6},
			{Text: "spotify", Weight: 1.6},
			{Text: "music video", Weight: 1.6},
			{Text: "top 40", Weight: 1.6},
			{Text: "music charts", Weight: 1.4},
			{Text: "songwriter", Weight: 1.4},
			{Text: "radio play", Weight: 1.4},
			{Text: "airplay", Weight: 1.4},
			{Text: "remix", Weight: 1.4},
			{Text: "mastering", Weight: 1.4},
			{Text: "dj set", Weight: 1.4},
			{Text: "live performance", Weight: 1.4},
			{Text: "solo artist", Weight: 1.2},
			{Text: "music industry", Weight: 1.2},
			{Text: "ticket sales", Weight: 1.0},
			{Text: "hip hop", Weight: 1.2},
			{Text: "symphony", Weight: 1.2},
			{Text: "classical music", Weight: 1.2},
			{Text: "opera", Weight: 1.2},
			{Text: "lyricist", Weight: 1.2},
			{Text: "pop star", Weight: 1.0},
			{Text: "rock band", Weight: 1.0},
			{Text: "orchestra", Weight: 1.0},
			{Text: "conductor", Weight: 1.0},
			{Text: "jazz", Weight: 1.0},
			{Text: "mixing", Weight: 0.8, Requires: []string{
				"studio", "track", "song", "album", "producer", "engineer", "mastering",
			}},
			{Text: "band", Weight: 0.6},
		},
		MinScore: 0,
		Prompt: "Assign for musicians and the recorded-music industry: releases, tours, charts, " +
			"labels. Not for a film's score considered as part of the film (see Film & TV), and not " +
			"for concert-venue business unrelated to a specific act.",
	}
}
