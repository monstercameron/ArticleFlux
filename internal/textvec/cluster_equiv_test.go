package textvec

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
)

// naiveCluster is AgglomerativeCluster as it was before the similarity matrix:
// recompute every surviving pair after every merge.
//
// Kept as the oracle. The rewrite is a pure performance change and the only
// claim worth making about it is that it produces the same clusters — which is
// a claim about two implementations, so both have to be here to make it.
//
// It is intentionally the ORIGINAL code, not a clean-room reimplementation. A
// reimplementation would be a second thing that can be wrong, and the two being
// wrong in the same way is exactly the failure this test exists to rule out.
func naiveCluster(vs []Vector, threshold float64) []Cluster {
	if len(vs) == 0 {
		return nil
	}
	clusters := make([]Cluster, len(vs))
	for i, v := range vs {
		c := make(Vector, len(v))
		for t, w := range v {
			c[t] = w
		}
		clusters[i] = Cluster{Members: []int{i}, Centroid: c}
	}

	for len(clusters) > 1 {
		bestI, bestJ, best := -1, -1, threshold
		for i := 0; i < len(clusters); i++ {
			for j := i + 1; j < len(clusters); j++ {
				if s := Cosine(clusters[i].Centroid, clusters[j].Centroid); s > best {
					bestI, bestJ, best = i, j, s
				}
			}
		}
		if bestI < 0 {
			break
		}
		merged := mergeCluster(clusters[bestI], clusters[bestJ])
		clusters = append(clusters[:bestJ], clusters[bestJ+1:]...)
		clusters[bestI] = merged
	}

	for i := range clusters {
		sort.Ints(clusters[i].Members)
	}
	sort.Slice(clusters, func(i, j int) bool {
		if len(clusters[i].Members) != len(clusters[j].Members) {
			return len(clusters[i].Members) > len(clusters[j].Members)
		}
		return clusters[i].Members[0] < clusters[j].Members[0]
	})
	return clusters
}

// TestClusteringMatchesTheNaiveImplementation compares membership, cluster for
// cluster, across a range of sizes and thresholds.
//
// # Why membership and not the centroids
//
// Because the centroids are floating-point sums over Go maps, and map iteration
// order is randomised per run. `dot` therefore accumulates its terms in a
// different order on every execution, so two runs of the SAME implementation can
// differ in the last bits of a centroid weight. That was true before this
// change and is true after it; asserting bitwise equality would be asserting
// something neither version has ever guaranteed.
//
// Membership is the output that anything downstream reads — topics.Build turns
// it into item ids — and it is stable as long as the merge ORDER is, which is
// the property the rewrite actually had to preserve.
//
// # Why several thresholds
//
// A threshold nothing clears exercises the initial matrix and zero merges. A
// threshold everything clears merges down to one cluster and exercises the
// shrink path n times. Only the middle values exercise both, and only they can
// catch a row/column deletion that is off by one — which would show as clusters
// merging in the wrong order rather than as a crash.
func TestClusteringMatchesTheNaiveImplementation(t *testing.T) {
	for _, n := range []int{2, 3, 7, 25, 60} {
		for _, threshold := range []float64{0.02, 0.1, 0.25, 0.5, 0.9} {
			t.Run(fmt.Sprintf("n=%d/threshold=%g", n, threshold), func(t *testing.T) {
				vs := equivVectors(n)
				got := AgglomerativeCluster(vs, threshold)
				want := naiveCluster(equivVectors(n), threshold)

				if len(got) != len(want) {
					t.Fatalf("got %d clusters, the naive implementation produced %d",
						len(got), len(want))
				}
				for i := range got {
					if !reflect.DeepEqual(got[i].Members, want[i].Members) {
						t.Errorf("cluster %d members = %v, naive = %v",
							i, got[i].Members, want[i].Members)
					}
				}
			})
		}
	}
}

// TestClusteringHandlesDegenerateInput covers the shapes a corpus produces at
// the edges, where an index arithmetic error is most likely to surface as a
// panic rather than as a wrong answer.
func TestClusteringHandlesDegenerateInput(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := AgglomerativeCluster(nil, 0.3); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("one vector", func(t *testing.T) {
		got := AgglomerativeCluster([]Vector{{"a": 1}}, 0.3)
		if len(got) != 1 || !reflect.DeepEqual(got[0].Members, []int{0}) {
			t.Errorf("got %+v, want one cluster holding index 0", got)
		}
	})
	// An empty vector has no terms, so Cosine is zero against everything and it
	// can never merge. It reaches here from a document that was entirely
	// stopwords, which is a real thing a headline can be.
	t.Run("empty vectors among real ones", func(t *testing.T) {
		vs := []Vector{{}, {"a": 1, "b": 1}, {}, {"a": 1, "b": 1}}
		got := AgglomerativeCluster(vs, 0.3)
		want := naiveCluster([]Vector{{}, {"a": 1, "b": 1}, {}, {"a": 1, "b": 1}}, 0.3)
		if len(got) != len(want) {
			t.Fatalf("got %d clusters, naive produced %d", len(got), len(want))
		}
		for i := range got {
			if !reflect.DeepEqual(got[i].Members, want[i].Members) {
				t.Errorf("cluster %d members = %v, naive = %v", i, got[i].Members, want[i].Members)
			}
		}
	})
	// Identical vectors all merge, which drives the shrink path to a single
	// cluster and is the case where an off-by-one in the column deletion has the
	// most chances to be caught.
	t.Run("all identical", func(t *testing.T) {
		vs := make([]Vector, 8)
		for i := range vs {
			vs[i] = Vector{"a": 1, "b": 2, "c": 3}
		}
		got := AgglomerativeCluster(vs, 0.3)
		if len(got) != 1 {
			t.Fatalf("got %d clusters, want 1: identical vectors are similarity 1", len(got))
		}
		if len(got[0].Members) != 8 {
			t.Errorf("cluster holds %d members, want 8", len(got[0].Members))
		}
	})
}

// equivVectors builds a deterministic set of overlapping sparse vectors.
//
// Overlapping is the point: vectors drawn from disjoint vocabularies have
// similarity zero and never merge, so the comparison would pass on an
// implementation that never merged anything at all.
func equivVectors(n int) []Vector {
	r := rand.New(rand.NewPCG(7, 11))
	out := make([]Vector, n)
	for i := range out {
		v := Vector{}
		// 12 terms drawn from 40, so any two vectors share several.
		for range 12 {
			v[fmt.Sprintf("t%d", r.IntN(40))] += 1 + r.Float64()
		}
		out[i] = v
	}
	return out
}
