package cast

import (
	"testing"
	"time"
)

// pool builds a plausible feed: a lead, then a long tail across three themes.
func pool(n int) []Story {
	themes := []string{"ai", "hardware", "world"}
	roles := []string{"SUPPORTING", "STANDARD", "STANDARD", "QUICK_HIT"}
	out := []Story{{ItemID: "lead", Role: "LEAD", Theme: "world", Title: "The lead", Source: "A"}}
	for i := 0; i < n-1; i++ {
		out = append(out, Story{
			ItemID: "i" + itoa(i),
			Role:   roles[i%len(roles)],
			Theme:  themes[i%len(themes)],
			Title:  "Headline " + itoa(i),
			Source: "Source " + itoa(i%4),
		})
	}
	return out
}

func planFmt(t *testing.T, f Format, p Profile, stories []Story) (Program, []Note) {
	t.Helper()
	return Plan(Input{
		ID: "show", Title: "Fitted", Stories: stories, Profile: p, Format: f, Rate: 1,
		Variant: Variant{Seed: 11, PartOfDay: "evening", Date: "1 August"},
	})
}

func within(got, want time.Duration, frac float64) bool {
	return absDur(got-want) <= time.Duration(float64(want)*frac)
}

func TestAFixedLengthBulletinLandsOnItsTarget(t *testing.T) {
	// The whole point of a format with a target: a bulletin that runs to
	// nineteen or twenty-three minutes depending on the feed is not a bulletin.
	f := BulletinFormat(20 * time.Minute)
	prog, notes := planFmt(t, f, Default(), pool(60))

	got := prog.PlannedLength()
	if !within(got, 20*time.Minute, f.Tolerance) {
		t.Errorf("planned %s against a 20m target (tolerance %.0f%%); notes: %v",
			got, f.Tolerance*100, notes)
	}
	// …and it should not have needed to complain to get there.
	for _, n := range notes {
		if n.Path == "format.target" {
			t.Errorf("the fitter reported it could not hit the target: %s", n)
		}
	}
}

func TestAnImpossibleTargetIsReportedRatherThanMissedSilently(t *testing.T) {
	// Two stories cannot fill forty minutes at any word budget a story can
	// carry. The honest answer is to say so — a fitter that silently produced
	// a 4,000-word "story" would hide the format's problem behind a number
	// that looked right.
	f := BulletinFormat(40 * time.Minute)
	prog, notes := planFmt(t, f, Default(), pool(2))

	if prog.PlannedLength() > 20*time.Minute {
		t.Errorf("two stories were stretched to %s", prog.PlannedLength())
	}
	var told bool
	for _, n := range notes {
		if n.Path == "format.target" || n.Path == "format.main" {
			told = true
		}
	}
	if !told {
		t.Errorf("the shortfall was not reported: %v", notes)
	}
}

func TestWordBudgetsStayInsideTheirRoleBounds(t *testing.T) {
	// The fitter may miss a target; it may not write a forty-word lead.
	f := BulletinFormat(6 * time.Minute)
	prog, _ := planFmt(t, f, Default(), pool(40))
	for _, b := range prog.Beats {
		if !b.Kind.Spoken() {
			continue
		}
		// Bounds are relative to the beat's own base budget, which is what
		// stops two fitting passes compounding into a lead trimmed twice.
		lo, hi := roleBounds(Default(), b)
		if b.Words < lo || b.Words > hi {
			t.Errorf("%s %s fitted to %d words, outside [%d, %d] for a base of %d",
				b.Kind, b.Role, b.Words, lo, hi, b.Base)
		}
	}
}

func TestABlockTakesOnlyWhatItsFilterAdmits(t *testing.T) {
	f := Format{
		Name: "two blocks", Tolerance: 0.2,
		Blocks: []Block{
			{Name: "lead", Kind: BlockStories, Take: 1, Filter: Filter{Roles: []string{"LEAD"}}},
			{Name: "ai only", Kind: BlockStories, Filter: Filter{Themes: []string{"ai"}}},
		},
	}
	prog, _ := planFmt(t, f, Default(), pool(20))
	for _, b := range prog.Beats {
		switch b.Block {
		case "lead":
			if b.Role != "LEAD" {
				t.Errorf("the lead block took a %s story", b.Role)
			}
		case "ai only":
			if b.Theme != "ai" {
				t.Errorf("the ai block took a %s story", b.Theme)
			}
		}
	}
	// Everything the format had no room for is reported rather than dropped
	// silently.
	if len(prog.Unused) == 0 {
		t.Error("a filtered format used every story in a twenty-story pool")
	}
}

func TestATeaseNamesWhatIsComingAndARecapWhatIsDone(t *testing.T) {
	f := Format{
		Name: "magazine-ish", Tolerance: 0.2,
		Blocks: []Block{
			{Name: "first", Kind: BlockStories, Take: 2},
			{Name: "coming up", Kind: BlockTease, Lineup: 2, Look: 1},
			{Name: "second", Kind: BlockStories, Take: 2},
			{Name: "recap", Kind: BlockRecap, Lineup: 3},
		},
	}
	prog, _ := planFmt(t, f, Default(), pool(10))

	var tease, recap Beat
	for _, b := range prog.Beats {
		switch b.Kind {
		case BeatTease:
			tease = b
		case BeatRecap:
			recap = b
		}
	}
	if len(tease.Lineup) == 0 {
		t.Fatal("the tease named nothing")
	}
	if len(recap.Lineup) == 0 {
		t.Fatal("the recap named nothing")
	}
	// A tease that named a story already played would be telling a listener
	// about something they have just heard.
	played := map[string]bool{}
	for _, b := range prog.Beats {
		if b.Kind == BeatStory {
			played[b.ItemID] = true
		}
		if b.Kind == BeatTease {
			for _, id := range b.Lineup {
				if played[id] {
					t.Errorf("the tease named %s, which had already played", id)
				}
			}
		}
		if b.Kind == BeatRecap {
			for _, id := range b.Lineup {
				if !played[id] {
					t.Errorf("the recap named %s, which had not played yet", id)
				}
			}
		}
	}
}

func TestATeaseWithNothingToNameIsDropped(t *testing.T) {
	f := Format{
		Name: "trailing tease", Tolerance: 0.2,
		Blocks: []Block{
			{Name: "stories", Kind: BlockStories, Take: 2},
			{Name: "coming up", Kind: BlockTease, Lineup: 2},
		},
	}
	prog, notes := planFmt(t, f, Default(), pool(5))
	for _, b := range prog.Beats {
		if b.Kind == BeatTease {
			t.Error("a tease survived with nothing after it to name")
		}
	}
	if !hasNote(notes, "format.coming up") {
		t.Errorf("the dropped tease was not reported: %v", notes)
	}
}

func TestAnAnchoredBlockIsPaddedToItsOffset(t *testing.T) {
	// "The recap is at ten minutes" has to mean ten minutes, whatever the
	// first half ran to.
	f := Format{
		Name: "anchored", Tolerance: 0.1,
		Blocks: []Block{
			{Name: "first", Kind: BlockStories, Take: 2},
			{Name: "recap", Kind: BlockRecap, Lineup: 2, At: Dur(10 * time.Minute), Pad: true},
			{Name: "rest", Kind: BlockStories, Take: 2},
		},
	}
	prog, _ := planFmt(t, f, Default(), pool(10))

	var at time.Duration
	for _, b := range prog.Beats {
		if b.Block == "recap" {
			at += b.PadBefore
			break
		}
		at += b.PadBefore + b.Est
	}
	at += choreoOverhead(beatsBefore(prog.Beats, "recap"), prog.Profile)
	if !within(at, 10*time.Minute, 0.02) {
		t.Errorf("the anchored recap starts at %s, want 10m", at)
	}
}

func TestAnAnchorThatCannotBeMetSaysSo(t *testing.T) {
	// Nothing can start earlier than the material in front of it, and a
	// running order that silently moved its own anchor would have stopped
	// being a schedule.
	f := Format{
		Name: "impossible", Tolerance: 0.1,
		Blocks: []Block{
			{Name: "first", Kind: BlockStories, Take: 20},
			{Name: "recap", Kind: BlockRecap, Lineup: 2, At: Dur(30 * time.Second), Pad: true},
		},
	}
	_, notes := planFmt(t, f, Default(), pool(30))
	if !hasNote(notes, "format.recap") {
		t.Errorf("an unreachable anchor was not reported: %v", notes)
	}
}

func beatsBefore(beats []Beat, block string) []Beat {
	for i, b := range beats {
		if b.Block == block {
			return beats[:i]
		}
	}
	return beats
}

func TestABreakPlaysNothingAndEndsOnItsOwnClock(t *testing.T) {
	f := Format{
		Name: "with a break", Tolerance: 0.2,
		Blocks: []Block{
			{Name: "first", Kind: BlockStories, Take: 1},
			{Name: "break", Kind: BlockBreak, Target: Dur(8 * time.Second), Music: MusicFeature},
			{Name: "second", Kind: BlockStories, Take: 1},
		},
	}
	prog, _ := planFmt(t, f, Default(), pool(6))
	b := prog.Beats
	if b[1].Kind != BeatBreak {
		t.Fatalf("beat 1 is %s, want a break", b[1].Kind)
	}
	if b[1].Est != 8*time.Second {
		t.Errorf("the break runs %s, want the 8s it was scheduled for", b[1].Est)
	}

	r := newRun(t, prog)
	r.play(b[0].ID, 30*time.Second)
	mark := len(r.acts)
	// Nothing is fetched or played FOR the break itself.
	for _, a := range r.acts[mark:] {
		if a.Kind == ActPlay && a.BeatID == b[1].ID {
			t.Error("the player tried to play a break")
		}
	}
	r.advance(4 * time.Second)
	if cur, _ := r.p.Current(); cur.ID != b[1].ID {
		t.Fatalf("mid-break the player is on %s", cur.ID)
	}
	r.advance(6 * time.Second)
	if cur, _ := r.p.Current(); cur.ID != b[2].ID {
		t.Errorf("after the break the player is on %s, want %s", cur.ID, b[2].ID)
	}
}

func TestATeaseIsNotCreditedAsHeard(t *testing.T) {
	// Crediting a tease would mark three articles read because somebody was
	// told they were coming.
	f := Format{
		Name: "teased", Tolerance: 0.2,
		Blocks: []Block{
			{Name: "first", Kind: BlockStories, Take: 1},
			{Name: "coming up", Kind: BlockTease, Lineup: 2, Look: 1},
			{Name: "second", Kind: BlockStories, Take: 2},
		},
	}
	prog, _ := planFmt(t, f, Default(), pool(8))
	b := prog.Beats
	r := newRun(t, prog)
	for _, beat := range b {
		if beat.Kind.Spoken() {
			r.play(beat.ID, 10*time.Second)
			r.advance(4 * time.Second)
		}
	}
	if got, want := r.count(ActHeard), 3; got != want {
		t.Errorf("credited %d beats as heard, want %d (the stories only)", got, want)
	}
}

func TestTheWordBudgetIsPartOfTheCacheKey(t *testing.T) {
	// The same story fitted to two different lengths is two different
	// recordings. Sharing one key would mean a fitted programme quietly
	// playing unfitted audio.
	long, _ := planFmt(t, BulletinFormat(30*time.Minute), Default(), pool(12))
	short, _ := planFmt(t, BulletinFormat(6*time.Minute), Default(), pool(12))

	keyOf := func(p Program, item string) (string, int) {
		for _, b := range p.Beats {
			if b.Kind == BeatStory && b.ItemID == item {
				return b.Key, b.Words
			}
		}
		return "", 0
	}
	lk, lw := keyOf(long, "lead")
	sk, sw := keyOf(short, "lead")
	if lw == sw {
		t.Fatalf("both shows gave the lead %d words; the fitter did nothing", lw)
	}
	if lk == sk {
		t.Error("two different word budgets share one cache key")
	}
}

func TestCalibrationMeasuresWhatTheVoiceActuallyDid(t *testing.T) {
	// The half of "accurate timings" that no amount of planning supplies: what
	// this synthesiser, in this voice, actually reads at.
	prog, _ := planFmt(t, FeedFormat(), Default(), pool(4))
	r := newRun(t, prog)
	for _, b := range prog.Beats {
		if !b.Kind.Spoken() {
			continue
		}
		// Every beat runs at exactly half the estimated pace, so the measured
		// rate must come back at about half the assumed 150.
		r.on(EvReady, b.ID)
		r.on(EvPlaying, b.ID)
		r.advance(b.Est * 2)
		r.on(EvEnded, b.ID)
		r.advance(4 * time.Second)
	}
	cal, ok := r.p.Trace().Calibrate(prog)
	if !ok {
		t.Fatal("nothing was measured")
	}
	if cal.Samples < 4 {
		t.Errorf("measured over %d beats, want every spoken one", cal.Samples)
	}
	want := Default().Editorial.WordsPerMinute / 2
	if cal.WordsPerMinute < want*0.9 || cal.WordsPerMinute > want*1.1 {
		t.Errorf("measured %.1f wpm, want about %.1f — %s", cal.WordsPerMinute, want, cal)
	}
}

func TestCalibrationBacksOutTheListenersRate(t *testing.T) {
	// A 1.5x listener heard the same prose in two thirds of the time. The
	// VOICE did not change, and a calibration that forgot that would ratchet
	// every future estimate by the listener's own preference.
	prog, _ := Plan(Input{ID: "fast", Stories: pool(3), Profile: Default(),
		Format: FeedFormat(), Rate: 1.5})
	r := newRun(t, prog)
	for _, b := range prog.Beats {
		if !b.Kind.Spoken() {
			continue
		}
		r.on(EvReady, b.ID)
		r.on(EvPlaying, b.ID)
		r.advance(b.Est) // exactly as planned, at 1.5x
		r.on(EvEnded, b.ID)
		r.advance(4 * time.Second)
	}
	cal, ok := r.p.Trace().Calibrate(prog)
	if !ok {
		t.Fatal("nothing was measured")
	}
	want := Default().Editorial.WordsPerMinute
	if cal.WordsPerMinute < want*0.85 || cal.WordsPerMinute > want*1.15 {
		t.Errorf("measured %.1f wpm for a show that ran exactly to plan at 1.5x, want about %.1f",
			cal.WordsPerMinute, want)
	}
}

func TestEveryStockFormatPlansAndPlays(t *testing.T) {
	// A stock format that cannot survive its own planner is worse than no
	// stock format.
	for _, name := range Formats() {
		f := FormatByName(name)
		prog, _ := planFmt(t, f, Default(), pool(30))
		if len(prog.Beats) == 0 {
			t.Errorf("format %q planned nothing", name)
			continue
		}
		tl := Compile(prog)
		var sum time.Duration
		for _, s := range tl.Steps {
			sum += s.Length
		}
		if sum != tl.Total {
			t.Errorf("format %q: steps sum to %s but the total is %s", name, sum, tl.Total)
		}
		// And it plays to the end without stalling.
		r := newRun(t, prog)
		for _, b := range prog.Beats {
			if b.Kind.Spoken() {
				r.play(b.ID, b.Est)
			}
			r.advance(12 * time.Second)
		}
		if !r.p.Done() {
			t.Errorf("format %q did not finish; phase %s", name, r.p.Phase())
		}
	}
}
