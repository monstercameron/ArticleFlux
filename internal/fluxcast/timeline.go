package fluxcast

import (
	"strconv"
	"strings"
	"time"
)

// StepKind is what one line of a cue sheet is.
type StepKind string

const (
	// StepSpeech is the narrator saying a beat.
	StepSpeech StepKind = "speech"
	// StepMusic is one instruction to the mix.
	StepMusic StepKind = "music"
	// StepHold is time with no voice in it, on purpose: the theme's phrase
	// before the news, the lift between stories. A hold is a decision, and
	// giving it its own line is what lets somebody see that a twenty-story
	// show spends a minute of itself on seams.
	StepHold StepKind = "hold"
	// StepEnd closes the sheet.
	StepEnd StepKind = "end"
)

// A Step is one line of the plan: when it happens, what it is, and why.
type Step struct {
	At     time.Duration
	Kind   StepKind
	BeatID string
	ItemID string
	// Label is what the sheet prints in its middle column — "LEAD · ai",
	// "OPENING", "seam".
	Label string
	// Length is how long this step occupies. For speech it is the estimate,
	// and the sheet marks it as one: a word budget is a request to a model,
	// not a contract with it.
	Length time.Duration
	Music  MusicCue
	Why    string
}

// A Timeline is a compiled programme: every cue, with the time it is planned
// for, and the total the show is expected to run.
//
// It is a PLAN, not a schedule. Two of the things in it are network round trips
// and a third is a model writing prose, so the actual times will differ — which
// is exactly why the plan is worth having, because a Trace can be laid against
// it and the difference is the tuning signal. Anything that claimed to be a
// schedule here would be lying about what it can know.
type Timeline struct {
	Program Program
	Steps   []Step
	// Total is when the last sound stops, music tail included.
	Total time.Duration
	// Voiced is how much of Total is somebody talking. The ratio of the two is
	// the single most useful number about a tuning: a programme that is 60%
	// music is an ambient display, and one that is 98% voice is a queue with a
	// jingle.
	Voiced time.Duration
}

// Compile lays a programme out in time.
//
// Pure and total: it never fails, because there is no input a running order can
// carry that makes a plan impossible — an empty programme compiles to an empty
// sheet, which is the honest answer rather than an error.
func Compile(p Program) Timeline {
	tl := Timeline{Program: p}
	prof := p.Profile
	mix := prof.Mix.Enabled

	var t time.Duration
	add := func(s Step) {
		s.At = t
		tl.Steps = append(tl.Steps, s)
	}
	music := func(c MusicCue) {
		add(Step{Kind: StepMusic, Music: c, Why: c.Why})
	}

	if len(p.Beats) == 0 {
		add(Step{Kind: StepEnd, Label: "nothing to broadcast", Why: "an empty running order is a state, not an error"})
		return tl
	}

	// The opening theme starts before anything is said. It is the first thing
	// anybody hears, and it covers the wait for a greeting that has to be
	// written and synthesised before it can play.
	if mix {
		music(openTheme(prof, p.Variant.Opening))
	}

	for i, b := range p.Beats {
		// A pad is dead air somebody scheduled, to land an anchored block on
		// its offset. It gets its own line because a sheet that folded it into
		// the seam would hide a minute of the programme waiting for a clock.
		if b.PadBefore > 0 {
			add(Step{Kind: StepHold, Label: "pad → " + b.Block, Length: b.PadBefore,
				Why: "holding for the anchor on " + b.Block})
			t += b.PadBefore
		}
		switch b.Kind {
		case BeatOpening:
			if mix {
				music(duckTheme(prof))
			}
			add(Step{
				Kind: StepSpeech, BeatID: b.ID, ItemID: b.ItemID,
				Label: "OPENING", Length: b.Est,
				Why: "the greeting, the date, and what is coming",
			})
			t += b.Est

			// The interlude: the theme has the room, then leaves as the bed
			// arrives, and the news begins INTO the bed rather than on top of
			// the fade.
			if mix {
				music(swellTheme(prof))
				add(Step{Kind: StepHold, Label: "theme", Length: prof.Choreo.IntroHold.D(),
					Why: "the theme's own phrase, which is what makes this a programme rather than a queue"})
				t += prof.Choreo.IntroHold.D()
				out, in := crossToBed(prof, "")
				music(out)
				music(in)
				add(Step{Kind: StepHold, Label: "crossfade", Length: prof.Choreo.IntroLead.D(),
					Why: "the news starts into a bed that is already there"})
				t += prof.Choreo.IntroLead.D()
			}

		case BeatStory:
			if mix {
				music(duckBed(prof))
			}
			label := strings.ToUpper(b.Role)
			if label == "" {
				label = "STORY"
			}
			if b.Theme != "" {
				label += " · " + b.Theme
			}
			add(Step{
				Kind: StepSpeech, BeatID: b.ID, ItemID: b.ItemID,
				Label: label, Length: b.Est,
				Why: "words " + strconv.Itoa(b.Words) + ", estimated at " +
					strconv.FormatFloat(prof.Editorial.WordsPerMinute, 'g', 4, 64) + " wpm",
			})
			t += b.Est
			tl.Voiced += b.Est

			// The seam, but only between two stories. The last story hands
			// over to the sign-off, which is not a seam — the music does
			// something else entirely there.
			if next, ok := p.Next(b.ID); ok && next.Kind == BeatStory {
				if mix {
					music(seamBed(prof))
				}
				add(Step{Kind: StepHold, Label: "seam", Length: prof.Choreo.SeamHold.D(),
					Why: "the programme breathing; the next story starts into the lift rather than on top of it"})
				t += prof.Choreo.SeamHold.D()
			}

		case BeatTease, BeatRecap:
			if mix {
				music(duckBed(prof))
			}
			label := "COMING UP"
			if b.Kind == BeatRecap {
				label = "RECAP"
			}
			add(Step{
				Kind: StepSpeech, BeatID: b.ID, ItemID: b.ItemID,
				Label: label, Length: b.Est,
				Why: "names " + strconv.Itoa(len(b.Lineup)) + " stories",
			})
			t += b.Est
			tl.Voiced += b.Est

		case BeatBreak:
			if mix {
				music(MusicCue{Channel: ChannelBed, Op: MusicLevel, Level: prof.Mix.BedSeam,
					Over: prof.Mix.BedSwell, Why: "the break: the music has the room"})
			}
			add(Step{Kind: StepHold, Label: "break", Length: b.Est,
				Why: "scheduled music, with nothing said over it"})
			t += b.Est

		case BeatSignOff:
			add(Step{
				Kind: StepSpeech, BeatID: b.ID, ItemID: b.ItemID,
				Label: "SIGN-OFF", Length: b.Est,
				Why: "the difference between it finished and it broke",
			})
			t += b.Est
			tl.Voiced += b.Est
			if mix {
				music(outroBed(prof))
				add(Step{Kind: StepHold, Label: "outro", Length: prof.Mix.OutroRise.D() + prof.Mix.OutroFall.D(),
					Why: "the music has the last ten seconds; the reader is already free"})
				t += prof.Mix.OutroRise.D() + prof.Mix.OutroFall.D()
			}
		}
		_ = i
	}

	// The opening's own words count as voice too, and they were added before
	// Voiced existed in the loop above — done here so the loop reads as the
	// programme rather than as bookkeeping.
	for _, b := range p.Beats {
		if b.Kind == BeatOpening {
			tl.Voiced += b.Est
		}
	}

	tl.Total = t
	add(Step{Kind: StepEnd, Label: "end", Why: "the programme is over"})
	return tl
}

// Sheet renders the running order as something a person reads.
//
// This is the artefact the whole engine exists to produce. Before it, the only
// way to find out what a tuning did was to play twenty minutes of radio and
// try to remember the first half; the sheet is the same information in forty
// lines, and two of them can be diffed.
func (t Timeline) Sheet() string {
	var b strings.Builder
	p := t.Program
	b.WriteString(p.Title)
	if p.Title == "" {
		b.WriteString("(untitled programme)")
	}
	b.WriteString("\n")
	b.WriteString("profile " + p.Profile.Name +
		" · vibe " + p.Variant.Vibe +
		" · rate " + strconv.FormatFloat(p.Rate, 'g', 3, 64) +
		" · seed " + strconv.FormatUint(p.Variant.Seed, 10) + "\n")
	b.WriteString(strconv.Itoa(p.Stories()) + " stories · planned " + fmtDur(t.Total))
	if t.Total > 0 {
		pct := int(100 * t.Voiced.Seconds() / t.Total.Seconds())
		b.WriteString(" · " + strconv.Itoa(pct) + "% voice")
	}
	b.WriteString("\n\n")

	for _, s := range t.Steps {
		b.WriteString(fmtDur(s.At))
		b.WriteString("  ")
		switch s.Kind {
		case StepSpeech:
			b.WriteString(pad(s.Label, 24))
			b.WriteString(" ~" + fmtDur(s.Length))
			b.WriteString("  " + s.ItemID)
		case StepMusic:
			b.WriteString(pad("· "+s.Music.String(), 24))
		case StepHold:
			b.WriteString(pad("· hold "+s.Label, 24))
			b.WriteString("  " + fmtDur(s.Length))
		case StepEnd:
			b.WriteString(pad("— "+s.Label, 24))
		}
		if s.Why != "" {
			b.WriteString("   " + s.Why)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func pad(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}
