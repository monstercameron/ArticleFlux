package demodata

import (
	"context"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The demo's interest profile: what My Feed "thinks", and the dial over it
// (§18.2, §18.9).
//
// # What is real here and what is not
//
// The real thing derives clusters and named entities from an engagement log,
// and there is no log in a tab that has been open for ninety seconds. So the
// topics and the things-you-follow are FIXTURES — chosen, not computed — and
// the dial over them genuinely works: pressing Never marks the row, and the
// factor mix underneath recounts without it.
//
// The fixtures are chosen to teach the screen's actual lesson rather than to
// flatter it. Two of them describe the reader; one is a MISREAD — "Apple Watch",
// three mentions and a reading weight that dwarfs them, which is one review read
// closely and is not a subject anybody follows. That row is the whole reason the
// screen exists, and a demo whose model is uniformly right would demonstrate a
// feature nobody needs.
//
// The feeds and the factor mix ARE computed, from the demo's own ranking (see
// rankMegafeed), because those two are the parts a reader can check against the
// list in the next tab.

// interest is the mutable half: the dial, keyed the way the RPC addresses it.
//
// Held on the Instance rather than in the fixtures so that a reset is a new
// Instance, like everything else here.
type interest struct {
	topicLevel  map[string]pb.SteerLevel
	entityLevel map[string]pb.SteerLevel
}

type demoTopic struct {
	id      string
	label   string
	terms   []string
	members int32
	trend   string
}

type demoEntity struct {
	name     string
	label    string
	kind     string
	weight   float64
	mentions int32
}

// demoTopics are three clusters, and the third is deliberately junk.
//
// "Read · Time · Min" is the shape a vectoriser produces when it eats the
// furniture around an article rather than the article — a real failure this
// application has had — and it is here so the screen has something worth turning
// down on the first visit.
var demoTopics = []demoTopic{
	{
		id: "topic-databases", label: "Databases and migrations",
		terms:   []string{"migration", "schema", "query", "index", "postgres"},
		members: 14, trend: "rising",
	},
	{
		id: "topic-fieldwork", label: "Fieldwork and measurement",
		terms:   []string{"sample", "series", "sensor", "station", "orbit"},
		members: 9, trend: "steady",
	},
	{
		id: "topic-furniture", label: "Read · Time · Min",
		terms:   []string{"read", "time", "min", "comments", "share"},
		members: 6, trend: "fading",
	},
}

// demoEntities lead with the misread, because the list is sorted by weight and
// that is exactly how the real one surfaces it: the strongest "thing you follow"
// is the one thing you read hardest, which is not the same as a subject.
var demoEntities = []demoEntity{
	{name: "apple watch", label: "Apple Watch", kind: "phrase", weight: 31.4, mentions: 3},
	{name: "quiet systems", label: "Quiet Systems", kind: "phrase", weight: 18.2, mentions: 11},
	{name: "postgres", label: "Postgres", kind: "phrase", weight: 12.6, mentions: 8},
	{name: "orbital digest", label: "Orbital Digest", kind: "phrase", weight: 7.1, mentions: 6},
	{name: "health check", label: "Health Check", kind: "phrase", weight: 2.4, mentions: 2},
}

func newInterest() *interest {
	return &interest{
		topicLevel:  map[string]pb.SteerLevel{},
		entityLevel: map[string]pb.SteerLevel{},
	}
}

// level reads a dial that may never have been touched. Unspecified is not a
// position — see view.effectiveLevel, which draws the same conclusion.
func level(m map[string]pb.SteerLevel, key string) pb.SteerLevel {
	if l, ok := m[key]; ok && l != pb.SteerLevel_STEER_LEVEL_UNSPECIFIED {
		return l
	}
	return pb.SteerLevel_STEER_LEVEL_NORMAL
}

func (s *readerService) GetInterestProfile(_ context.Context, _ *pb.GetInterestProfileRequest) (*pb.GetInterestProfileResponse, error) {
	in := s.inst
	in.count("GetInterestProfile")
	in.mu.Lock()
	defer in.mu.Unlock()

	out := &pb.GetInterestProfileResponse{
		TopicCount:  int32(len(demoTopics)),
		EntityCount: int32(len(demoEntities)),
	}
	for _, t := range demoTopics {
		out.Topics = append(out.Topics, &pb.InterestTopic{
			Id: t.id, Label: t.label, Terms: t.terms,
			MemberCount: t.members, Trend: t.trend,
			Level: level(in.interest.topicLevel, topicKeyOf(t)),
			// The demo's ids are stable, so a key is redundant here — and it is
			// sent anyway, because a fake whose handles work differently from the
			// real one's teaches a client habit that breaks in production.
			Key: topicKeyOf(t),
		})
	}
	for _, e := range demoEntities {
		out.Entities = append(out.Entities, &pb.InterestEntity{
			Name: e.name, Label: e.label, Kind: e.kind,
			Weight: e.weight, Mentions: e.mentions,
			Level: level(in.interest.entityLevel, e.name),
		})
	}

	ranked := in.rankedForProfile()
	out.RankedCount = int32(len(ranked))
	// The demo counts its factors over the same list it just built, so the two
	// are the same number here — sent all the same, because a fake that leaves a
	// field empty is one the client learns to work around.
	out.FactorBase = int32(len(ranked))
	out.Feeds = in.profileFeeds(ranked)
	out.Factors = in.profileFactors(ranked)
	// The demo's clusters are fixtures, so they always "contribute" — the cold
	// start band would be a lie here rather than a state worth demonstrating.
	out.ColdStart = false
	return out, nil
}

func (s *readerService) SteerInterest(_ context.Context, req *pb.SteerInterestRequest) (*pb.SteerInterestResponse, error) {
	in := s.inst
	in.count("SteerInterest")
	in.mu.Lock()
	defer in.mu.Unlock()

	topic, entity := req.GetTopicKey(), req.GetEntityName()
	// The same argument check the server makes, and it is here for the reason
	// this whole package exists: a fake that accepts what the real one rejects
	// teaches a client habit that fails in production.
	if (topic == "") == (entity == "") {
		return nil, status.Error(codes.InvalidArgument,
			"steer: give exactly one of topic_key or entity_name")
	}
	if req.GetLevel() == pb.SteerLevel_STEER_LEVEL_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "steer: no level given")
	}

	if topic != "" {
		if !knownTopic(topic) {
			return nil, status.Error(codes.NotFound, "no such topic")
		}
		in.interest.topicLevel[topic] = req.GetLevel()
	} else {
		name := strings.ToLower(strings.TrimSpace(entity))
		if !knownEntity(name) {
			return nil, status.Error(codes.NotFound, "no such thing")
		}
		in.interest.entityLevel[name] = req.GetLevel()
	}
	// False, and honestly so: there is no deriver behind this tab, and the
	// profile it returns next is recomputed on the spot. The client says "saved"
	// rather than "saved, rebuilding" on the strength of this.
	return &pb.SteerInterestResponse{Rebuilding: false}, nil
}

// topicKeyOf mirrors store.TopicKey: the first three terms, lowercased and
// joined by a unit separator.
//
// Duplicated rather than imported: this package must not depend on the store
// (the demo has no database), and the value only has to be self-consistent
// here — nothing crosses between a demo tab and a real instance.
func topicKeyOf(t demoTopic) string {
	terms := t.terms
	if len(terms) > 3 {
		terms = terms[:3]
	}
	return strings.ToLower(strings.Join(terms, "\x1f"))
}

func knownTopic(key string) bool {
	for _, t := range demoTopics {
		if topicKeyOf(t) == key {
			return true
		}
	}
	return false
}

func knownEntity(name string) bool {
	for _, e := range demoEntities {
		if e.name == name {
			return true
		}
	}
	return false
}

// rankedForProfile is the ranked page as the profile counts it: unread items
// from megafeed feeds, in the order rankMegafeed puts them.
//
// The caller holds the lock.
func (in *Instance) rankedForProfile() []*item {
	var out []*item
	for _, it := range in.items {
		if it.read || !it.feed.inMegafeed {
			continue
		}
		out = append(out, it)
	}
	in.rankMegafeed(out)
	return out
}

// profileFeeds is affinity as this demo can honestly express it: how much of the
// ranked page each feed won, which is the same claim the real score makes and is
// arrived at differently.
func (in *Instance) profileFeeds(ranked []*item) []*pb.InterestFeed {
	picks := map[string]int{}
	for _, it := range ranked {
		picks[it.feed.sourceID]++
	}
	top := 1
	for _, n := range picks {
		if n > top {
			top = n
		}
	}

	out := make([]*pb.InterestFeed, 0, len(in.feeds))
	for _, f := range in.feeds {
		shown, opened := 0, 0
		for _, it := range in.items {
			if it.feed != f {
				continue
			}
			shown++
			if it.read {
				opened++
			}
		}
		if shown == 0 {
			continue
		}
		out = append(out, &pb.InterestFeed{
			SourceId:    f.sourceID,
			Title:       f.title(),
			Score:       float64(picks[f.sourceID]) / float64(top),
			Opens:       int32(opened),
			Impressions: int32(shown),
			OnMyFeed:    f.inMegafeed,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].GetScore() > out[j].GetScore() })
	return out
}

// profileFactors counts the judgements behind the demo's own ranking.
//
// Derived from the same three things rankMegafeed scores on — the interest
// weight, the age, and the deliberate acts — so the histogram describes the list
// the reader can go and look at rather than a story about it. A steer that sets
// something to Never removes its factor here, which is what makes the screen's
// cause and effect visible in a demo with no deriver.
func (in *Instance) profileFactors(ranked []*item) []*pb.InterestFactor {
	now := in.clock()
	counted := map[string]int{}
	topicsOn := 0
	for _, t := range demoTopics {
		if level(in.interest.topicLevel, t.id) != pb.SteerLevel_STEER_LEVEL_NEVER {
			topicsOn++
		}
	}
	for _, it := range ranked {
		if now.Sub(it.published).Hours() < 48 {
			counted["fresh"]++
		}
		if it.weight >= 6 && topicsOn > 0 {
			counted["topic"]++
		}
		if it.feed.inMegafeed && it.weight >= 4 {
			counted["feed"]++
		}
		if it.starred || it.note != "" || it.rating > 0 {
			counted["deliberate"]++
		}
		// The named things, counted only while they are still counted. Matching
		// on the headline is what the real one does too (see derive.namedIn).
		lower := strings.ToLower(it.title + " " + it.feed.title())
		for _, e := range demoEntities {
			if level(in.interest.entityLevel, e.name) == pb.SteerLevel_STEER_LEVEL_NEVER {
				continue
			}
			if strings.Contains(lower, e.name) {
				counted["entity"]++
				break
			}
		}
	}

	out := make([]*pb.InterestFactor, 0, len(counted))
	for term, n := range counted {
		out = append(out, &pb.InterestFactor{Term: term, Items: int32(n)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].GetItems() != out[j].GetItems() {
			return out[i].GetItems() > out[j].GetItems()
		}
		return out[i].GetTerm() < out[j].GetTerm()
	})
	return out
}
