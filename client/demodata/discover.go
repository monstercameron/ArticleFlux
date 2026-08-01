package demodata

import (
	"context"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/recommend"
)

// Discover (§18.7) in the demo — the four RPCs behind Settings → Discover.
//
// # It runs the real scorer
//
// The lazy version of this file is a list of finished cards: a domain, a
// sentence, a number. It would render identically and it would be a lie, because
// the thing worth demonstrating about Discover is not that a card can be drawn —
// it is that every card carries evidence assembled from the same figures that
// produced its score, and that the health gate refuses more candidates than it
// accepts. A fixture of finished cards demonstrates neither; it demonstrates
// that somebody can type a sentence into a struct.
//
// So the fixtures below are CANDIDATES — what a harvest observed — and
// internal/recommend.Score turns them into recommendations here, exactly as
// internal/recommendjob does on a server, with the same defaulted thresholds. The
// evidence strings on screen are written by describe(); the ordering is the
// scorer's; the three candidates that never appear are refused by gate() rather
// than by being left out of the list. That is the same argument client/demodata
// makes for speaking gRPC to itself, one layer up: route through the shipping
// code, and a demo that works is evidence about the application.
//
// What the demo genuinely cannot do is harvest. Rungs 1–3 read outlinks,
// aggregator engagement and blogrolls out of a store this instance does not
// have, and rungs 4–5 are Smart+. So the observations are seeded — and they are
// seeded to be consistent with the reading fixture next door, which is what
// makes the evidence checkable: "3 writers you read linked here" names writers
// who are in the rail.

// recCandidate is one seeded observation, plus the two things the fixture needs
// that recommend.Candidate cannot carry.
//
// lastPostH and postsPerWeek are the health figures. lastPostH is relative
// because every timestamp in this package is relative to the instance's clock —
// see New's clock parameter — and a candidate last seen in a hardcoded month of
// 2026 would age into the silence rejection on its own.
type recCandidate struct {
	domain  string
	title   string
	rung    recommend.Rung
	ev      recommend.Evidence
	hasFeed bool
	// reachable is separate from hasFeed: a site that answered and has no feed
	// and a site that did not answer are different rejections, and the gate
	// says so.
	reachable    bool
	isAggregator bool
	lastPostH    int
	postsPerWeek float64

	// pass is which scoring pass turned this up. 0 is the harvest that had
	// already run when the demo opened; 1 is what Refresh finds. The button is
	// most of what this screen does between visits, and a Refresh that never
	// finds anything teaches the wrong thing about it — the same reason
	// seedItems holds four articles back for the article Refresh.
	pass int
}

// seedCandidates is the harvest.
//
// Four survive the gate on the first pass, two more arrive with Refresh, and
// three are refused — one silent, one firehose, one with no feed behind it.
// The refusals are the point of including them: §18.7's argument is that a bad
// recommendation costs more than a missed one, and a fixture where everything
// is accepted argues the opposite of the feature it is demonstrating.
//
// Every domain is under a reserved .example, and the writers named in the
// evidence are the fixture's own — see seed.go on why nothing here is real.
var seedCandidates = []recCandidate{
	{
		domain: "smallsystems.example", title: "Notes on Small Systems",
		rung: recommend.RungOutlinks, hasFeed: true, reachable: true,
		lastPostH: 30, postsPerWeek: 2,
		ev: recommend.Evidence{
			LinkCount: 11, DistinctSources: 3, EngagementWeight: 2.4,
		},
	},
	{
		domain: "deadletter.example", title: "The Dead Letter Queue",
		rung: recommend.RungAggregator, hasFeed: true, reachable: true,
		lastPostH: 9, postsPerWeek: 5,
		ev: recommend.Evidence{
			StarredViaAggregator: 4, AggregatorName: "Hackline Daily",
			LinkCount: 2, DistinctSources: 2, EngagementWeight: 0.9,
		},
	},
	{
		domain: "theslowweb.example", title: "The Slow Web",
		rung: recommend.RungBlogroll, hasFeed: true, reachable: true,
		lastPostH: 5 * 24, postsPerWeek: 1,
		ev: recommend.Evidence{
			FromBlogrollOf: "Quiet Systems", LinkCount: 3, DistinctSources: 2,
			EngagementWeight: 1.1,
		},
	},
	{
		// The guardrail slot (§18.7): deliberately unlike the rest of the list,
		// so a recommender optimised purely for similarity does not quietly
		// become a filter bubble with better manners. It scores lowest of the
		// four and is reserved a slot anyway, which is the behaviour worth
		// showing.
		domain: "kilnandpress.example", title: "Kiln & Press",
		rung: recommend.RungOutlinks, hasFeed: true, reachable: true,
		lastPostH: 3 * 24, postsPerWeek: 1.5,
		ev: recommend.Evidence{
			LinkCount: 2, DistinctSources: 2, EngagementWeight: 0.5, Adjacent: true,
		},
	},

	// --- refused by the health gate ------------------------------------------
	{
		// Silent for fourteen months. Recommending a dead site is how a reader
		// learns the feature does not check anything.
		domain: "archivedthoughts.example", title: "Archived Thoughts",
		rung: recommend.RungOutlinks, hasFeed: true, reachable: true,
		lastPostH: 14 * 30 * 24, postsPerWeek: 0.2,
		ev: recommend.Evidence{LinkCount: 6, DistinctSources: 2, EngagementWeight: 1.4},
	},
	{
		// A firehose. Subscribing would bury the rail in an afternoon.
		domain: "wirefeed.example", title: "Wirefeed",
		rung: recommend.RungAggregator, hasFeed: true, reachable: true,
		lastPostH: 1, postsPerWeek: 900,
		ev: recommend.Evidence{StarredViaAggregator: 2, AggregatorName: "Hackline Daily"},
	},
	{
		// Linked often, and there is nothing to subscribe to. The most common
		// rejection on a real instance by a wide margin.
		domain: "conferencetalks.example", title: "Conference Talks",
		rung: recommend.RungOutlinks, reachable: true, hasFeed: false,
		lastPostH: 40, postsPerWeek: 3,
		ev: recommend.Evidence{LinkCount: 9, DistinctSources: 4, EngagementWeight: 2.0},
	},

	// --- what Refresh finds ---------------------------------------------------
	{
		domain: "groundtruth.example", title: "Ground Truth",
		rung: recommend.RungOutlinks, hasFeed: true, reachable: true,
		lastPostH: 2, postsPerWeek: 3, pass: 1,
		ev: recommend.Evidence{LinkCount: 5, DistinctSources: 2, EngagementWeight: 1.6},
	},
	{
		domain: "theorbitlog.example", title: "The Orbit Log",
		rung: recommend.RungAggregator, hasFeed: true, reachable: true,
		lastPostH: 20, postsPerWeek: 4, pass: 1,
		ev: recommend.Evidence{
			StarredViaAggregator: 2, AggregatorName: "Orbital Digest",
			LinkCount: 1, DistinctSources: 1, EngagementWeight: 0.4,
		},
	},
}

// feedURLFor is the address a candidate's subscribe button would use. The
// harvest validated it, which is why AcceptRecommendation does not have to.
func (c recCandidate) feedURL() string {
	if !c.hasFeed {
		return ""
	}
	return "https://" + c.domain + "/feed.xml"
}

// candidate renders the fixture as the scorer's input.
func (c recCandidate) candidate(now time.Time) recommend.Candidate {
	h := recommend.Health{
		HasFeed:      c.hasFeed,
		Reachable:    c.reachable,
		IsAggregator: c.isAggregator,
		PostsPerWeek: c.postsPerWeek,
	}
	if c.lastPostH > 0 {
		h.LastPostAt = now.Add(-time.Duration(c.lastPostH) * time.Hour)
	}
	return recommend.Candidate{
		Domain: c.domain, FeedURL: c.feedURL(), Title: c.title,
		Rung: c.rung, Health: h, Evidence: c.ev,
	}
}

// score runs the pass. Caller holds the lock.
//
// Rejections are returned alongside the survivors because the Activity screen
// says how many of each there were, and "9 scored, 4 kept" is the sentence that
// makes the health gate visible on a screen that otherwise only shows what
// survived it.
func (in *Instance) score() recommend.Result {
	now := in.clock()

	// What the reader has already decided. Subscribed comes off the live feed
	// list rather than a copy, so accepting a suggestion — or typing the same
	// address into Add feed by hand — removes it from the list for the same
	// reason on the next load.
	known := map[string]recommend.State{}
	for _, f := range in.feeds {
		known[strings.ToLower(hostOf(f.feedURL))] = recommend.State{Subscribed: true}
	}
	for d := range in.recDismissed {
		st := known[d]
		st.Dismissed = true
		known[d] = st
	}

	cands := make([]recommend.Candidate, 0, len(seedCandidates))
	for _, c := range seedCandidates {
		if c.pass > in.recPasses {
			continue
		}
		cands = append(cands, c.candidate(now))
	}
	// Thresholds{} — the same empty value internal/recommendjob passes, so the
	// gate's limits here are the deployed ones rather than a demo's opinion.
	return recommend.Score(cands, known, recommend.Thresholds{}, now)
}

func (s *readerService) ListRecommendations(_ context.Context, _ *pb.ListRecommendationsRequest) (
	*pb.ListRecommendationsResponse, error) {

	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	res := in.score()
	out := &pb.ListRecommendationsResponse{
		Recommendations: make([]*pb.Recommendation, 0, len(res.Recommendations)),
	}
	for _, r := range res.Recommendations {
		out.Recommendations = append(out.Recommendations, &pb.Recommendation{
			Domain: r.Domain, FeedUrl: r.FeedURL, Title: r.Title,
			Score: r.Score, Rung: int32(r.Rung), Evidence: r.Evidence,
		})
	}
	return out, nil
}

// RefreshRecommendations runs another pass.
//
// On a server this enqueues a job and returns — the harvest is a batch of
// requests to other people's machines, and the client re-polls. The demo has no
// job pool and nothing to fetch, so it does the honest local equivalent: it
// advances the pass counter, which releases the candidates a second harvest
// would have turned up, and returns. The client re-lists either way, so the
// shape the screen sees is the server's shape.
//
// The second Refresh finds nothing new, which is also true of a real one most
// of the time and is worth a demo reader seeing.
func (s *readerService) RefreshRecommendations(_ context.Context, _ *pb.RefreshRecommendationsRequest) (
	*pb.RefreshRecommendationsResponse, error) {

	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	before := len(in.score().Recommendations)
	in.recPasses++
	res := in.score()
	in.logf("INFO", "scored recommendation candidates",
		"kept", strconv.Itoa(len(res.Recommendations)),
		"rejected", strconv.Itoa(len(res.Rejected)),
		"new", strconv.Itoa(max(0, len(res.Recommendations)-before)))
	return &pb.RefreshRecommendationsResponse{}, nil
}

// AcceptRecommendation subscribes to the suggestion's already-validated feed
// URL, which is why there is no address to re-check here.
func (s *readerService) AcceptRecommendation(_ context.Context, req *pb.AcceptRecommendationRequest) (
	*pb.AcceptRecommendationResponse, error) {

	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	domain := strings.ToLower(strings.TrimSpace(req.GetDomain()))
	if domain == "" {
		return nil, status.Error(codes.InvalidArgument, "domain is required")
	}

	// Against the OPEN list rather than the fixture, so a domain that has been
	// dismissed, already subscribed to, or refused by the health gate cannot be
	// accepted through an id the screen no longer offers.
	open := in.score().Recommendations
	var rec *recommend.Recommendation
	for i := range open {
		if open[i].Domain == domain {
			rec = &open[i]
			break
		}
	}
	if rec == nil || rec.FeedURL == "" {
		return nil, status.Error(codes.NotFound, "no open recommendation for that domain")
	}

	// Nothing marks the domain accepted afterwards, and nothing needs to: the
	// subscription IS the record. score() builds its Subscribed set from the
	// live feed list, so the card is gone from the next pass for the reason the
	// reader would give — they follow it now.
	f := in.addFeed(rec.FeedURL, rec.Title, "", "feed")
	in.stockItems(f, 3)
	in.resort()
	return &pb.AcceptRecommendationResponse{
		Feed: f.message(in.unreadFor(f.sourceID)),
	}, nil
}

// RejectRecommendation is permanent (§18.7). A recommender that re-asks is one
// the reader learns to ignore, so the domain goes into a set the gate reads on
// every pass — including the ones Refresh runs.
func (s *readerService) RejectRecommendation(_ context.Context, req *pb.RejectRecommendationRequest) (
	*pb.RejectRecommendationResponse, error) {

	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	domain := strings.ToLower(strings.TrimSpace(req.GetDomain()))
	if domain == "" {
		return nil, status.Error(codes.InvalidArgument, "domain is required")
	}
	in.recDismissed[domain] = true
	return &pb.RejectRecommendationResponse{}, nil
}
