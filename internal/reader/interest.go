package reader

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The interest profile: what the ranking believes, and how a reader says it is
// wrong (plan.md §18.2, §18.9).
//
// # Why this is a screen and not a row of dismiss buttons
//
// Every My Feed row already carries its reasons, and a per-row "not this" would
// have been cheaper to build. It answers the wrong question. A reader looking at
// one row can tell that the row is odd; they cannot tell that a single phrase is
// deciding forty of them. The failure this exists for is exactly that shape —
// on a real database the strongest followed thing was "Pro Max", weight 37 from
// two mentions, which is one handset review read closely and is not a subject
// anybody follows.
//
// So the unit here is the MODEL, not the article: the topics, the named things,
// the feeds, and the mix of factors behind the current page. Correcting one of
// those fixes every row it touches, and — because the corrections survive
// derivation (0027) — keeps fixing them.
//
// # Assembled here rather than in the transport
//
// Four store reads and a histogram. It lives in the service layer because the
// cold-start rule and the factor counting are judgements about the ranking, not
// about the wire, and because the offline-pack channel and the REST API get the
// same answer without reimplementing either.

// MaxProfileEntities bounds the entity list.
//
// Sixty. The store caps the table at derive.MaxEntities (150), and a settings
// screen listing all of them is a screen nobody reads to the end — but the cap
// has to be well clear of the number a reader has actually corrected, or their
// own "never" rows scroll off the bottom and look deleted. Sixty is roughly
// three screens, and the response says what the total was.
const MaxProfileEntities = 60

// MaxProfileFeeds is how many subscriptions the profile names.
//
// Twelve. This is not the feed list — the sidebar is — it is "which feeds are
// winning the ranked page", and past a dozen the answer is noise: affinity below
// the top ten or so is dominated by how much each feed happens to publish.
const MaxProfileFeeds = 12

// InterestProfile is the whole of what the interest layer thinks about one
// reader, in one answer.
type InterestProfile struct {
	Topics   []store.TopicRow
	Entities []store.Entity
	Feeds    []ProfileFeed
	Factors  []ProfileFactor
	// Ranked is how many picks are on the page — the summary line's own number,
	// counted in SQL rather than by paging (see store.CountRanked).
	Ranked int
	// FactorBase is how many of those the factor histogram was computed over.
	// Equal to Ranked except on a page past store.MaxRankedPage.
	FactorBase int
	// ColdStart is true when no ranked pick cites a topic.
	//
	// The same rule the list's own "still learning" band uses, and deliberately
	// not a threshold on the engagement count: a reader can have two hundred
	// engagements spread so thinly that no cluster forms, and another can have
	// sixty on one subject and get three good topics. "Did any topic actually
	// contribute?" is the question, and the rows are where the answer is.
	ColdStart bool
	// TopicCount, EntityCount and FeedCount are the totals before truncation, so
	// a capped list can say what it is a slice of — and so the summary line
	// counts what exists rather than what fitted.
	TopicCount  int
	EntityCount int
	FeedCount   int
}

// ProfileFeed is one subscription's standing on the ranked page.
type ProfileFeed struct {
	SourceID     string
	Title        string
	Score        float64
	Opens        int
	Impressions  int
	VolumePerDay float64
	// OnMyFeed is false for a feed the reader excluded (in_megafeed) or muted.
	OnMyFeed bool
}

// ProfileFactor is how many current picks cited one scoring term.
type ProfileFactor struct {
	Term  string
	Items int
}

// InterestProfile assembles it.
func (s *Service) InterestProfile(ctx context.Context, sc store.Scope) (InterestProfile, error) {
	var p InterestProfile

	topicRows, err := s.repo.Topics(ctx, sc)
	if err != nil {
		return p, err
	}
	p.Topics = topicRows
	p.TopicCount = len(topicRows)

	// AllEntities, not EntityAffinity: a thing the reader struck out has to stay
	// on the screen that struck it out, or the correction cannot be undone.
	ents, err := s.repo.AllEntities(ctx, sc, MaxProfileEntities)
	if err != nil {
		return p, err
	}
	p.Entities = ents
	// The cap is on the query, so the total is a second question. Asking it only
	// when the list came back full keeps the common case to one read.
	p.EntityCount = len(ents)
	if len(ents) == MaxProfileEntities {
		all, err := s.repo.AllEntities(ctx, sc, 500)
		if err != nil {
			return p, err
		}
		p.EntityCount = len(all)
	}

	feeds, total, err := s.profileFeeds(ctx, sc)
	if err != nil {
		return p, err
	}
	p.Feeds = feeds
	p.FeedCount = total

	// The factor mix, from the ranking that is actually on screen rather than
	// from a fresh scoring pass. A page explaining itself has to explain the page
	// the reader is looking at; re-deriving here would describe a ranking nobody
	// has seen and could differ from it on any freshness term.
	//
	// RankedItems, not HomeRanking, and the difference is the whole reason this
	// line is commented. HomeRanking returns every materialised row; RankedItems
	// applies the filters the LIST applies — unread, still subscribed, both rows
	// live — which is also what CountRanked counts. Reading the raw table gave
	// "Recent 103 of 102": a numerator counted over one set and a denominator
	// over another, on a screen whose entire claim is that its numbers can be
	// checked.
	ranked, _, err := s.repo.RankedItems(ctx, sc, 0, store.MaxRankedPage)
	if err != nil {
		return p, err
	}
	counted := map[string]int{}
	for _, row := range ranked {
		// Once per ITEM per term. rank emits at most one reason per term today,
		// but counting occurrences rather than items would make the number mean
		// something else the day that changes — and "37 of 99 picks" is only a
		// sentence if the numerator counts picks.
		seen := map[string]bool{}
		for _, r := range row.Reasons {
			if r.Term == "" || seen[r.Term] {
				continue
			}
			seen[r.Term] = true
			counted[r.Term]++
		}
	}
	p.ColdStart = !citesATopic(ranked)

	p.Factors = make([]ProfileFactor, 0, len(counted))
	for term, n := range counted {
		p.Factors = append(p.Factors, ProfileFactor{Term: term, Items: n})
	}
	sort.Slice(p.Factors, func(i, j int) bool {
		if p.Factors[i].Items != p.Factors[j].Items {
			return p.Factors[i].Items > p.Factors[j].Items
		}
		return p.Factors[i].Term < p.Factors[j].Term
	})

	p.Ranked, err = s.repo.CountRanked(ctx, sc)
	if err != nil {
		return p, err
	}
	// The denominator the factor lines are OUT OF, which is the number of picks
	// actually counted rather than the number that exist. They differ only past
	// store.MaxRankedPage, and when they do, "37 of 200" over a page of 340 is
	// still a true sentence where "37 of 340" would not be.
	p.FactorBase = len(ranked)
	if p.FactorBase > p.Ranked && p.Ranked > 0 {
		p.FactorBase = p.Ranked
	}
	return p, nil
}

// citesATopic reports whether any pick matched a topic. See ColdStart.
func citesATopic(ranked []store.RankedItem) bool {
	for _, row := range ranked {
		if row.TopicID != "" {
			return true
		}
	}
	return false
}

// profileFeeds joins the derived affinities to the names a reader recognises.
//
// A feed with an affinity row and no subscription is skipped rather than shown
// under its id: unsubscribing leaves the derived row until the next rebuild, and
// a settings screen naming a feed the reader has already removed is a bug that
// looks like data loss.
//
// The second return is how many feeds have an affinity row at all, before the
// cap — the number the summary line means by "competing".
func (s *Service) profileFeeds(ctx context.Context, sc store.Scope) ([]ProfileFeed, int, error) {
	aff, err := s.repo.FeedAffinities(ctx, sc)
	if err != nil {
		return nil, 0, err
	}
	if len(aff) == 0 {
		return nil, 0, nil
	}
	feeds, err := s.repo.ListFeeds(ctx, sc)
	if err != nil {
		return nil, 0, err
	}
	eligible, err := s.repo.MegafeedSources(ctx, sc)
	if err != nil {
		return nil, 0, err
	}

	out := make([]ProfileFeed, 0, len(feeds))
	for _, f := range feeds {
		a, ok := aff[f.SourceID]
		if !ok {
			continue
		}
		out = append(out, ProfileFeed{
			SourceID:     f.SourceID,
			Title:        f.Title,
			Score:        a.Score,
			Opens:        a.Opens,
			Impressions:  a.Impressions,
			VolumePerDay: a.VolumePerDay,
			OnMyFeed:     eligible[f.SourceID],
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Title < out[j].Title
	})
	total := len(out)
	if len(out) > MaxProfileFeeds {
		out = out[:MaxProfileFeeds]
	}
	return out, total, nil
}

// SteerLevel is one position of the reader's dial, as the service speaks it.
type SteerLevel string

const (
	SteerMore   SteerLevel = "more"
	SteerNormal SteerLevel = "normal"
	SteerLess   SteerLevel = "less"
	SteerNever  SteerLevel = "never"
)

// weights turns a level into the pair the store stores.
//
// The mapping lives here rather than in the transport because it is a scoring
// decision: the reader says "more", and what more is WORTH is this application's
// judgement, revisable without a wire change or a client deploy.
func (l SteerLevel) weights() (steer float64, suppressed bool, ok bool) {
	switch l {
	case SteerMore:
		return store.SteerMore, false, true
	case SteerNormal:
		return store.SteerNormal, false, true
	case SteerLess:
		return store.SteerLess, false, true
	case SteerNever:
		// The multiplier is reset on the way to never, so coming back lands on
		// "normal" rather than on whatever was set three months ago. A dial that
		// remembers a position the screen stopped showing is one that surprises
		// the person who undoes their own correction.
		return store.SteerNormal, true, true
	}
	return 0, false, false
}

// SteerTopic records what the reader thinks of a cluster, addressed by its
// fingerprint rather than by its row id.
//
// # The id would have worked for one press
//
// Every derivation deletes and reinserts the whole topics table with fresh ids,
// and this method's own success schedules one. So a screen that steered by id
// steered correctly once, and answered `not found` for every press after the
// rebuild its first press kicked off — which is how a reader met "Could not read
// the profile: not found" seconds after their correction had actually landed.
// The key is what ReplaceTopics already carries corrections across by; nothing
// else in this application is entitled to a longer-lived handle than that.
//
// Returns whether a rebuild was requested. The correction changes nothing a
// reader can SEE until the next derivation reads it — the ranking is
// materialised — so the hook is fired for the same reason SetPrefs fires it: a
// control that produces no visible result is one people conclude is broken.
func (s *Service) SteerTopic(ctx context.Context, sc store.Scope, topicKey string, level SteerLevel) (bool, error) {
	steer, suppressed, ok := level.weights()
	if !ok {
		return false, fmt.Errorf("reader: unknown steer level %q", level)
	}
	if strings.TrimSpace(topicKey) == "" {
		return false, fmt.Errorf("reader: no topic")
	}
	// Resolved at write time, so the id used is the one that exists NOW rather
	// than the one the screen was rendered from.
	row, err := s.repo.TopicByKey(ctx, sc, topicKey)
	if err != nil {
		return false, err
	}
	if err := s.repo.SteerTopic(ctx, sc, row.ID, steer, suppressed); err != nil {
		return false, err
	}
	return s.rebuild(sc), nil
}

// SteerEntity records what the reader thinks of a named thing.
func (s *Service) SteerEntity(ctx context.Context, sc store.Scope, name string, level SteerLevel) (bool, error) {
	steer, suppressed, ok := level.weights()
	if !ok {
		return false, fmt.Errorf("reader: unknown steer level %q", level)
	}
	// Normalised the same way the derivation stores it, so a client echoing back
	// the label instead of the key still addresses the right row.
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false, fmt.Errorf("reader: no entity")
	}
	if err := s.repo.SteerEntity(ctx, sc, name, steer, suppressed); err != nil {
		return false, err
	}
	return s.rebuild(sc), nil
}

// TODO: renaming a topic. store.RenameTopic has existed since §18.2 and nothing
// calls it, because a rename needs a text field per row and this screen's rows
// are a delegated click surface with no per-row state. It is worth doing — a
// cluster called "Read · Time · Min" is one a reader can only argue with by
// turning it down — and it wants the tag panel's single-dialog shape rather than
// sixty inline fields.

// rebuild asks for a re-derivation and reports whether anything is listening.
func (s *Service) rebuild(sc store.Scope) bool {
	if s.onRankPrefs == nil {
		return false
	}
	s.onRankPrefs(sc)
	return true
}
