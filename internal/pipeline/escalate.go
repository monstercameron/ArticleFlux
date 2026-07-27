package pipeline

// The ambiguity gate (plan.md §27.4a, TODO 10.13).
//
// # This is the cost design, and it is built before the thing it gates
//
// The naive pipeline sends every item to the model. At 150 feeds and ~40 items a
// day that is ~6,000 requests a day for a self-hosted reader, which is not a
// feature, it is a subscription — and it spends the most on the items the free
// tier already gets right. A Hacker News post about SQLite does not need a
// language model to be filed under Software.
//
// So the deterministic pass runs first, always, and the model is asked only when
// the free tier says it is unsure. It can say that precisely because §27.3b made
// refusing an outcome and the margin a routing signal rather than a gate.
//
// The gate ships before the read (10.14) deliberately, so that `always` is never
// the shipped behaviour even briefly.
//
// # The property worth protecting
//
// **Spend falls as the lexicon improves.** Every corpus-driven fix in §27.11
// permanently removes a class of items from the escalation set. A pipeline that
// always calls the model has no such feedback loop and costs the same forever —
// which is the real argument for this design, more than the immediate saving.

// EscalatePolicy is the reader-facing knob.
type EscalatePolicy string

const (
	// EscalateNever is free tier only. The product still works; this is the first
	// lever in §27.12's cost list.
	EscalateNever EscalatePolicy = "never"
	// EscalateAmbiguous is the default: ask only where the free tier is unsure.
	EscalateAmbiguous EscalatePolicy = "ambiguous"
	// EscalateAlways reads every item that has something to read.
	EscalateAlways EscalatePolicy = "always"
)

// DefaultPolicy is what an instance gets without choosing.
const DefaultPolicy = EscalateAmbiguous

// MinEscalateWords is the floor below which there is nothing worth sending.
//
// Twenty-five words across the title and summary. Below that, with no body, the
// model is being asked to categorise a headline and a fragment — and §27.4a's
// table is explicit that this buys a coin flip at full price. The free tier's
// guess on the same text is free and no worse.
const MinEscalateWords = 25

// Reason names why the gate decided what it did.
//
// A typed reason rather than a bare bool, because this number turns into a bill.
// "31% of items escalated" is not actionable; "24% because they were unsorted and
// 5% because two categories tied" says which lexicon work would reduce it, which
// is the whole point of the feedback loop above.
type Reason string

const (
	// ReasonPolicyOff — the reader turned it off.
	ReasonPolicyOff Reason = "policy_off"
	// ReasonNoText — nothing worth sending (MinEscalateWords).
	ReasonNoText Reason = "no_text"
	// ReasonConfident — the free tier placed it with a clear margin.
	ReasonConfident Reason = "confident"
	// ReasonAlways — the reader asked for every item.
	ReasonAlways Reason = "always"
	// ReasonAmbiguous — a primary was assigned but the runner-up was close.
	ReasonAmbiguous Reason = "ambiguous"
	// ReasonUnsorted — nothing cleared its floor.
	ReasonUnsorted Reason = "unsorted"
	// ReasonNotEnglish — the free tier cannot read it and the model can.
	ReasonNotEnglish Reason = "not_english"
)

// Decision is the gate's answer, with its reason.
type Decision struct {
	Escalate bool
	Reason   Reason
}

// ShouldEscalate applies §27.4a's table to one analysed item.
//
// The order of the checks is the specification, not an implementation detail, and
// two of them are worth stating because they are not what a reading of the policy
// names would predict.
//
// **`no_text` beats `always`.** "Always" means "always where there is something
// to read"; there is no useful answer to buy for a six-word headline with no body,
// and a policy name should not be able to override the absence of input. Someone
// who sets `always` wants thorough, not wasteful.
//
// **A non-English item escalates even though the free tier declined it.** That
// looks like an exception and it is the cleanest case in the table: the free tier
// refused precisely because the shipped lexicon is English-only (§27.13), and a
// language model is exactly the thing that does not have that limitation. The
// item is not hard, it is out of the free tier's reach — which is the definition
// of work worth paying for.
func ShouldEscalate(a Analysis, it Item, p EscalatePolicy) Decision {
	if p == EscalateNever {
		return Decision{false, ReasonPolicyOff}
	}
	if !worthSending(it) {
		return Decision{false, ReasonNoText}
	}
	if p == EscalateAlways {
		return Decision{true, ReasonAlways}
	}
	if a.Lang != "" && a.Lang != LangEnglish {
		return Decision{true, ReasonNotEnglish}
	}
	if a.Primary == "" {
		return Decision{true, ReasonUnsorted}
	}
	if a.Ambiguous {
		return Decision{true, ReasonAmbiguous}
	}
	return Decision{false, ReasonConfident}
}

// worthSending reports whether there is enough text to buy an answer.
//
// The body counts as sufficient on its own without being measured, because an
// item that HAS a body has already been through extraction and is by construction
// more than a fragment. The word count only has to adjudicate the headline-only
// case, which is where the coin flips are.
func worthSending(it Item) bool {
	if len(it.Body) > 0 {
		return true
	}
	return countWords(it.Title)+countWords(it.Summary) >= MinEscalateWords
}

func countWords(s string) int {
	n, inWord := 0, false
	for _, r := range s {
		switch {
		case r == ' ' || r == '\n' || r == '\t' || r == '\r':
			inWord = false
		case !inWord:
			inWord = true
			n++
		}
	}
	return n
}

// EscalationSet is the outcome of gating a whole batch.
type EscalationSet struct {
	// Indexes into the batch, in order, that should be sent.
	Indexes []int
	// Reasons counts every decision by reason, escalated or not.
	//
	// Every item is counted, including the ones that were NOT sent. A counter that
	// only records escalations can say how much was spent and never why the rest
	// was skipped, and "why is my spend so high" is answered by the skips.
	Reasons map[Reason]int
}

// Share is the fraction of the batch that escalated.
func (e EscalationSet) Share(batch int) float64 {
	if batch == 0 {
		return 0
	}
	return float64(len(e.Indexes)) / float64(batch)
}

// Gate applies the policy across a batch.
//
// Returns indexes rather than a filtered slice so the caller can write each
// answer back to the item it belongs to. A filtered copy would need a parallel
// index anyway, and the version that forgets it silently pairs answers with the
// wrong articles — a failure that looks like a bad model rather than like a bug.
func Gate(analyses []Analysis, items []Item, p EscalatePolicy) EscalationSet {
	out := EscalationSet{Reasons: map[Reason]int{}}
	n := min(len(analyses), len(items))
	for i := range n {
		d := ShouldEscalate(analyses[i], items[i], p)
		out.Reasons[d.Reason]++
		if d.Escalate {
			out.Indexes = append(out.Indexes, i)
		}
	}
	return out
}

// KnownPolicy reports whether a stored setting is one this build understands.
//
// An unrecognised policy must fall back to the DEFAULT and not to `always`: a
// typo in a settings row, or a value written by a newer build, would otherwise
// start spending on every item — and the failure is invisible until the bill.
func KnownPolicy(p EscalatePolicy) bool {
	switch p {
	case EscalateNever, EscalateAmbiguous, EscalateAlways:
		return true
	}
	return false
}

// Policy resolves a stored string safely.
func Policy(s string) EscalatePolicy {
	p := EscalatePolicy(s)
	if KnownPolicy(p) {
		return p
	}
	return DefaultPolicy
}
