package smart

import (
	"context"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/derive"
)

// Every Smart+ feature bounds its own call, and the bound reaches the provider.
//
// This is regression coverage for something that was silently wrong in two
// different directions at once, and invisible from here in both.
//
// SchemaFlux applied a thirty-second default to some operations and not others:
// `Summarizing` and `Choosing` went through its operationContext and were
// capped, `Extracting` and `Generating` did not and were not. So this package
// had three kinds of call without knowing it —
//
//   - a feature with its own timeout SHORTER than thirty seconds, which worked
//     (categorise, at fifteen);
//   - a feature with its own timeout LONGER, which was silently cut to thirty
//     (nothing here, by luck: the interest pass runs at ninety and is an
//     Extracting, the one shape that was never capped);
//   - a feature with NO timeout of its own, which was either capped at thirty
//     or completely unbounded depending on which operation it happened to use.
//     Translate, scrape and the grouped podcast write were in the last group:
//     an Extracting with no deadline, which a stalled provider could hold open
//     indefinitely.
//
// SchemaFlux DX-008 made a caller's deadline authoritative, which turns every
// constant in this package from a suggestion into the answer. This test is what
// keeps it that way: it asserts the deadline the provider is handed comes from
// HERE, so a future library default cannot quietly take the decision back.
func TestEveryFeatureBoundsItsOwnCall(t *testing.T) {
	cases := []struct {
		name string
		want time.Duration
		run  func(t *testing.T, ctx context.Context)
	}{
		{"RerankCandidates", interestTimeout, func(t *testing.T, ctx context.Context) {
			in := configuredInterest(&fakeLLM{configured: true})
			_, _ = in.RerankCandidates(ctx, []derive.Candidate{{Title: "A"}}, derive.ProfileHint{}, 1)
		}},
		{"ExtractEntities", interestTimeout, func(t *testing.T, ctx context.Context) {
			in := configuredInterest(&fakeLLM{configured: true})
			_, _ = in.ExtractEntities(ctx, []string{"a headline"})
		}},
		{"LabelTopic", interestTimeout, func(t *testing.T, ctx context.Context) {
			in := configuredInterest(&fakeLLM{configured: true})
			_, _ = in.LabelTopic(ctx, []string{"sqlite", "btree"}, "Databases")
		}},
		{"Speakable", digestTimeout, func(t *testing.T, ctx context.Context) {
			d := NewDigest(&fakeLLM{configured: true}, nil, t.TempDir())
			_, _ = d.Speakable(ctx, "item-1", "LWN", "Fsyncgate", "the article body")
		}},
		{"Segment", segmentTimeout, func(t *testing.T, ctx context.Context) {
			p := NewPodcast(&fakeLLM{configured: true}, nil, t.TempDir())
			_, _ = p.Segment(ctx, Segment{ItemID: "item-1", Title: "T", Body: "the body"})
		}},
		{"WriteSegment", segmentGroupTimeout, func(t *testing.T, ctx context.Context) {
			p := NewPodcast(&fakeLLM{configured: true}, nil, t.TempDir())
			_, _ = p.WriteSegment(ctx, threeStoryGroup())
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := spyOn(t, "an answer of no particular shape")

			tc.run(t, context.Background())

			if spy.CallCount() == 0 {
				t.Fatal("the feature never reached the provider, so this proves nothing")
			}
			deadline, ok := spy.ContextN(0).Deadline()
			if !ok {
				t.Fatal("no deadline reached the provider; this call is unbounded and a " +
					"stalled provider would hold it open indefinitely")
			}

			// The caller passed a context with NO deadline, so whatever arrived
			// was put there by this package. Checking it is close to the
			// feature's own constant proves it came from here rather than from
			// a library default that happens to be in the same range.
			left := time.Until(deadline)
			if left > tc.want || left < tc.want-10*time.Second {
				t.Errorf("the provider was handed %s, want about %s (this feature's own budget). "+
					"A different number means something other than this package decided it",
					left.Round(time.Second), tc.want)
			}
		})
	}
}

// A caller's own deadline, when it is shorter, still wins.
//
// The per-feature constants are ceilings on a call this package makes, not
// floors it imposes on its caller: a background job that is being shut down
// hands in a nearly-expired context, and the right answer is to give up rather
// than to start a ninety-second request.
func TestAShorterCallerDeadlineBeatsTheFeaturesOwn(t *testing.T) {
	spy := spyOn(t, `{"picks":[{"id":1,"why":"fine"}]}`)

	short := 3 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), short)
	defer cancel()

	in := configuredInterest(&fakeLLM{configured: true})
	_, _ = in.RerankCandidates(ctx, []derive.Candidate{{Title: "A"}}, derive.ProfileHint{}, 1)

	if spy.CallCount() == 0 {
		t.Fatal("the feature never reached the provider")
	}
	deadline, ok := spy.ContextN(0).Deadline()
	if !ok {
		t.Fatal("no deadline reached the provider")
	}
	if left := time.Until(deadline); left > short {
		t.Errorf("the provider was handed %s of a %s caller budget; this package must not "+
			"extend a deadline its caller already set", left.Round(time.Second), short)
	}
}
