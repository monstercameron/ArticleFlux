package relabel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/propose"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The discovery pass, against a real database.
//
// `internal/propose` covers the gate exhaustively and purely. What is left here
// is everything the pure function cannot see: that the pile query excludes what
// it claims to, that a created category is actually usable afterwards, and that
// the probation ladder decides in both directions without anybody clicking
// anything.

// seedCluster writes n items whose vectors cluster, across `sources`, spread over
// `span` and ending now. Each gets an analysis row with a vector and NO category
// score, which is what makes it part of the uncategorised pile.
func seedCluster(t *testing.T, db *store.DB, repo *store.ReaderRepo, sc store.Scope,
	prefix string, n int, sources []string, span time.Duration) []string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)

	for _, src := range sources {
		_, _ = db.Write.ExecContext(ctx,
			`INSERT OR IGNORE INTO sources (id,natural_key,feed_url,created_at) VALUES (?,?,?,?)`,
			src, "feed:"+src, "https://"+src+".example/feed", stamp)
		_, _ = db.Write.ExecContext(ctx,
			`INSERT OR IGNORE INTO subscriptions (id,tenant_id,user_id,source_id,created_at) VALUES (?,?,?,?,?)`,
			"sub-"+src, sc.TenantID, sc.UserID, src, stamp)
	}

	ids := make([]string, 0, n)
	rows := make([]store.ItemAnalysis, 0, n)
	for i := range n {
		id := fmt.Sprintf("%s-%04d", prefix, i)
		ids = append(ids, id)
		var age time.Duration
		if n > 1 {
			age = time.Duration(float64(span) * float64(n-1-i) / float64(n-1))
		}
		published := now.Add(-age).Format(time.RFC3339Nano)
		if _, err := db.Write.ExecContext(ctx, `
			INSERT INTO items (id,source_id,guid,title,summary,published_at,first_seen_at)
			VALUES (?,?,?,?,?,?,?)`,
			id, sources[i%len(sources)], id,
			prefix+" council zoning", prefix+" council zoning budget hearing", published, stamp); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, store.ItemAnalysis{
			ItemID: id, AnalyzerVersion: 1, LexiconHash: "h", Lang: "en",
			// Empty: nothing cleared any built-in's floor. This is the pile.
			CategoryScores: map[string]float64{},
			// Terms that occur in the text above, because the centroid's heaviest
			// terms become the created category's include list and the scorer has
			// to be able to find them in an article.
			Vector: map[string]float64{
				prefix:    1.0,
				"council": 0.9,
				"zoning":  0.8,
				"budget":  0.4,
			},
			AnalyzedAt: now,
		})
	}
	if err := repo.UpsertAnalysis(ctx, rows); err != nil {
		t.Fatal(err)
	}
	return ids
}

// loose options for tests: the shipped MinMembers of 150 would mean seeding 150
// rows per test. The RULES under test here are the plumbing, not the thresholds —
// those are covered purely in internal/propose.
func testOptions() propose.Options {
	o := propose.Defaults()
	o.MinMembers = 20
	o.MinSpan = 30 * 24 * time.Hour
	return o
}

func TestDiscoveryCreatesAProbationaryCategoryThatThenFilesArticles(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seedCluster(t, db, repo, sc, "civic", 60, []string{"s1", "s2", "s3"}, 90*24*time.Hour)

	svc := New(repo, nil).WithPileFloor(20)
	res, err := svc.DiscoverFor(ctx, sc, testOptions())
	if err != nil {
		t.Fatalf("DiscoverFor: %v", err)
	}
	if len(res.Created) != 1 {
		t.Fatalf("created = %v, refused = %v", res.Created, res.Refused)
	}

	// It must arrive ON PROBATION, not active.
	deltas, err := repo.ListCategoryDeltas(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 1 {
		t.Fatalf("deltas = %d, want 1", len(deltas))
	}
	d := deltas[0]
	if d.State != store.CategoryProbation {
		t.Errorf("state = %q, want probation — a discovered label has never labelled anything", d.State)
	}
	if d.Origin != store.OriginDiscovered {
		t.Errorf("origin = %q, want discovered", d.Origin)
	}
	if len(d.Seed) == 0 {
		// Without the seed a later run cannot recompute this cluster's centroid,
		// and the novelty check silently stops recognising it.
		t.Error("no seed recorded")
	}
	if len(d.Include) == 0 {
		t.Fatal("no terms — the category cannot claim anything")
	}

	// And the whole point: the labelling sweep can now actually file articles
	// under it. Before 0034 this was impossible — a created category was inert.
	if _, err := svc.SweepUser(ctx, sc, 500); err != nil {
		t.Fatalf("SweepUser: %v", err)
	}
	counts, err := repo.CategoryCounts(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if counts[d.ID] == 0 {
		t.Fatal("the discovered category filed nothing; a category nothing can be placed in is not a feature")
	}
}

func TestDiscoveryIsSilentOnASmallPile(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seedCluster(t, db, repo, sc, "civic", 40, []string{"s1", "s2", "s3"}, 90*24*time.Hour)

	// Shipped MinPileForDiscovery, not the loosened options: an account two weeks
	// old has an uncategorised pile because it has barely any articles, not
	// because its taxonomy is wrong.
	res, err := New(repo, nil).DiscoverFor(ctx, sc, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("ran on a %d-item pile; the floor is %d", res.Pile, MinPileForDiscovery)
	}
	if len(res.Created) != 0 {
		t.Fatal("created a category from a pile too small to mean anything")
	}
}

func TestTheSamePassRunTwiceDoesNotCreateTwoCategories(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seedCluster(t, db, repo, sc, "civic", 60, []string{"s1", "s2", "s3"}, 90*24*time.Hour)

	svc := New(repo, nil).WithPileFloor(20)
	if _, err := svc.DiscoverFor(ctx, sc, testOptions()); err != nil {
		t.Fatal(err)
	}
	// The labelling sweep files the cluster under the new category, which takes
	// those items OUT of the uncategorised pile — the mechanism that stops the
	// second run seeing the same cluster at all.
	if _, err := svc.SweepUser(ctx, sc, 500); err != nil {
		t.Fatal(err)
	}

	res, err := svc.DiscoverFor(ctx, sc, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 0 {
		t.Fatalf("a second run created %v — this pass runs weekly, so a pass that re-proposes "+
			"its own last proposal produces a category a week forever", res.Created)
	}

	deltas, _ := repo.ListCategoryDeltas(ctx, sc)
	if len(deltas) != 1 {
		t.Fatalf("deltas = %d, want 1", len(deltas))
	}
}

func TestAProbationaryCategoryTheReaderKeepsCorrectingIsWithdrawn(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	ids := seedCluster(t, db, repo, sc, "civic", 60, []string{"s1", "s2", "s3"}, 90*24*time.Hour)

	d, err := repo.CreateCategory(ctx, sc, store.CategorySpec{
		Name:    "Civic",
		Origin:  store.OriginDiscovered,
		State:   store.CategoryProbation,
		Include: []classify.Term{{Text: "civic council", Weight: 4.0}},
		Seed:    ids[:10],
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(repo, nil).WithPileFloor(20)
	if _, err := svc.SweepUser(ctx, sc, 500); err != nil {
		t.Fatal(err)
	}

	// The reader removes it from a third of what it claimed.
	counts, _ := repo.CategoryCounts(ctx, sc)
	removals := counts[d.ID]/3 + 1
	for i := range removals {
		if _, err := db.Write.ExecContext(ctx, `
			INSERT INTO label_removals (tenant_id,user_id,item_id,kind,label_id,at)
			VALUES (?,?,?,'category',?,?)`,
			sc.TenantID, sc.UserID, ids[i], d.ID,
			time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}

	// Probation expires.
	backdate(t, db, d.ID, time.Now().UTC().Add(-ProbationPeriod-time.Hour))

	res, err := svc.DiscoverFor(ctx, sc, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Retired) != 1 {
		t.Fatalf("retired = %v, want the category the reader kept correcting", res.Retired)
	}

	// Withdrawn AND remembered. Retiring without recording means next week's run
	// re-finds the same cluster and proposes it again.
	props, err := repo.ListProposals(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 1 || props[0].Outcome != "retired" {
		t.Fatalf("proposals = %+v, want one 'retired' refusal", props)
	}
	if len(props[0].Seed) == 0 {
		t.Error("the refusal has no seed, so the cluster it refers to can never be recognised again")
	}
}

func TestAProbationaryCategoryThatEarnsItsKeepGraduates(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	ids := seedCluster(t, db, repo, sc, "civic", 60, []string{"s1", "s2", "s3"}, 90*24*time.Hour)

	d, err := repo.CreateCategory(ctx, sc, store.CategorySpec{
		Name:    "Civic",
		Origin:  store.OriginDiscovered,
		State:   store.CategoryProbation,
		Include: []classify.Term{{Text: "civic council", Weight: 4.0}},
		Seed:    ids[:10],
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(repo, nil).WithPileFloor(20)
	if _, err := svc.SweepUser(ctx, sc, 500); err != nil {
		t.Fatal(err)
	}
	backdate(t, db, d.ID, time.Now().UTC().Add(-ProbationPeriod-time.Hour))

	res, err := svc.DiscoverFor(ctx, sc, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Graduated) != 1 {
		t.Fatalf("graduated = %v, retired = %v — nobody removed anything from it", res.Graduated, res.Retired)
	}

	deltas, _ := repo.ListCategoryDeltas(ctx, sc)
	if deltas[0].State != store.CategoryActive {
		t.Fatalf("state = %q, want active", deltas[0].State)
	}
}

func TestAProbationaryCategoryThatFiledAlmostNothingExpires(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seedCluster(t, db, repo, sc, "civic", 60, []string{"s1", "s2", "s3"}, 90*24*time.Hour)

	// Terms that match nothing in the corpus, so it claims nothing.
	d, err := repo.CreateCategory(ctx, sc, store.CategorySpec{
		Name:    "Empty Room",
		Origin:  store.OriginDiscovered,
		State:   store.CategoryProbation,
		Include: []classify.Term{{Text: "quantum basketry", Weight: 4.0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(repo, nil).WithPileFloor(20)
	if _, err := svc.SweepUser(ctx, sc, 500); err != nil {
		t.Fatal(err)
	}
	backdate(t, db, d.ID, time.Now().UTC().Add(-ProbationPeriod-time.Hour))

	res, err := svc.DiscoverFor(ctx, sc, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Retired) != 1 {
		t.Fatalf("retired = %v, want the category that filed nothing", res.Retired)
	}

	props, _ := repo.ListProposals(ctx, sc)
	if len(props) != 1 || props[0].Outcome != "expired" {
		// Expiry means "too small" and retirement means "wrong". Only the second
		// is evidence about the reader's taste, so they must not be conflated.
		t.Fatalf("proposals = %+v, want one 'expired' record", props)
	}
}

func TestProbationIsNotJudgedBeforeItIsOver(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seedCluster(t, db, repo, sc, "civic", 60, []string{"s1", "s2", "s3"}, 90*24*time.Hour)

	d, err := repo.CreateCategory(ctx, sc, store.CategorySpec{
		Name:    "Empty Room",
		Origin:  store.OriginDiscovered,
		State:   store.CategoryProbation,
		Include: []classify.Term{{Text: "quantum basketry", Weight: 4.0}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Freshly created — it has claimed nothing yet, which is exactly the state a
	// premature judgement would kill it in.
	res, err := New(repo, nil).WithPileFloor(20).DiscoverFor(ctx, sc, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Retired) != 0 {
		t.Fatalf("retired %v before its probation was up", res.Retired)
	}
	deltas, _ := repo.ListCategoryDeltas(ctx, sc)
	for _, got := range deltas {
		if got.ID == d.ID {
			if got.State != store.CategoryProbation {
				t.Fatalf("state = %q, want it left alone until its probation is up", got.State)
			}
			return
		}
	}
	t.Fatal("the category vanished")
}

func TestThePileExcludesWhatNoClassifierCouldEverPlace(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, _ = db.Write.ExecContext(ctx,
		`INSERT INTO sources (id,natural_key,feed_url,created_at) VALUES ('s1','feed:s1','https://a.example/f',?)`, now)
	_, _ = db.Write.ExecContext(ctx,
		`INSERT INTO subscriptions (id,tenant_id,user_id,source_id,created_at) VALUES ('sub1',?,?,'s1',?)`,
		sc.TenantID, sc.UserID, now)

	mk := func(id string) {
		if _, err := db.Write.ExecContext(ctx, `
			INSERT INTO items (id,source_id,guid,title,published_at,first_seen_at)
			VALUES (?,'s1',?,?,?,?)`, id, id, id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"novector", "german", "analysed", "filed", "good"} {
		mk(id)
	}

	vec := map[string]float64{"alpha": 1}
	if err := repo.UpsertAnalysis(ctx, []store.ItemAnalysis{
		// No vector: a headline with no body. Nothing will ever place it.
		{ItemID: "novector", AnalyzerVersion: 1, LexiconHash: "h", Lang: "en",
			CategoryScores: map[string]float64{}, AnalyzedAt: time.Now().UTC()},
		// Not English: refused deliberately by §27.13. A language problem, not a
		// taxonomy one.
		{ItemID: "german", AnalyzerVersion: 1, LexiconHash: "h", Lang: "de",
			CategoryScores: map[string]float64{}, Vector: vec, AnalyzedAt: time.Now().UTC()},
		// Already filed by a built-in.
		{ItemID: "filed", AnalyzerVersion: 1, LexiconHash: "h", Lang: "en",
			CategoryScores: map[string]float64{"security": 9}, Vector: vec, AnalyzedAt: time.Now().UTC()},
		// A genuine refusal — the only one that belongs in the pile.
		{ItemID: "good", AnalyzerVersion: 1, LexiconHash: "h", Lang: "en",
			CategoryScores: map[string]float64{}, Vector: vec, AnalyzedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	// "analysed" deliberately gets NO analysis row: the analyzer's backlog, which
	// the rail counts as uncategorised and this pass must not.

	docs, err := repo.UncategorisedForDiscovery(ctx, sc, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ItemID != "good" {
		var got []string
		for _, d := range docs {
			got = append(got, d.ItemID)
		}
		t.Fatalf("pile = %v, want only [good] — the rail's number is four populations "+
			"wearing one label and three of them are noise to this pass", got)
	}
}

// backdate moves a category's probation start into the past, standing in for the
// three weeks a test cannot wait.
func backdate(t *testing.T, db *store.DB, id string, at time.Time) {
	t.Helper()
	if _, err := db.Write.ExecContext(context.Background(),
		`UPDATE categories SET state_at = ? WHERE id = ?`,
		at.Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryIsTriggeredByGrowthNotByAClock(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seedCluster(t, db, repo, sc, "civic", 60, []string{"s1", "s2", "s3"}, 90*24*time.Hour)
	svc := New(repo, nil).WithPileFloor(20)

	// Never run before: due immediately, rather than after a week of waiting.
	due, pile, err := svc.discoveryDue(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatalf("not due on a first look at a %d-item pile", pile)
	}

	if err := repo.RecordDiscoveryRun(ctx, sc, pile); err != nil {
		t.Fatal(err)
	}

	// Immediately after a run, an unchanged pile must NOT re-cluster. This is the
	// waste a weekly schedule cannot avoid and a growth trigger can.
	if due, _, err = svc.discoveryDue(ctx, sc); err != nil {
		t.Fatal(err)
	}
	if due {
		t.Fatal("due again with nothing new; the pass would re-refuse the same clusters forever")
	}

	// Backdate past the rate limit — still not due, because nothing arrived.
	backdateDiscovery(t, db, sc, time.Now().UTC().Add(-MinDiscoveryGap-time.Hour))
	if due, _, err = svc.discoveryDue(ctx, sc); err != nil {
		t.Fatal(err)
	}
	if due {
		t.Fatal("time alone made it due; a clock is not evidence that the corpus changed")
	}

	// Articles arrive. Now it is due.
	seedCluster(t, db, repo, sc, "sport", PileGrowthTrigger+10, []string{"s4", "s5", "s6"}, 90*24*time.Hour)
	if due, _, err = svc.discoveryDue(ctx, sc); err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatal("a pile that grew past the trigger did not become due")
	}
}

func TestGrowthCannotOverrideTheRateLimit(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seedCluster(t, db, repo, sc, "civic", 60, []string{"s1", "s2", "s3"}, 90*24*time.Hour)
	svc := New(repo, nil).WithPileFloor(20)

	_, pile, _ := svc.discoveryDue(ctx, sc)
	if err := repo.RecordDiscoveryRun(ctx, sc, pile); err != nil {
		t.Fatal(err)
	}

	// A bulk import: tens of thousands of articles in minutes. Without the gap,
	// this triggers a clustering pass on every tick while the backfill works
	// through them.
	seedCluster(t, db, repo, sc, "bulk", PileGrowthTrigger*3, []string{"s7", "s8", "s9"}, 90*24*time.Hour)

	due, _, err := svc.discoveryDue(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if due {
		t.Fatalf("a bulk import triggered a run inside MinDiscoveryGap (%v)", MinDiscoveryGap)
	}
}

func TestAQuietAccountIsStillReviewedEventually(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seedCluster(t, db, repo, sc, "civic", 60, []string{"s1", "s2", "s3"}, 90*24*time.Hour)
	svc := New(repo, nil).WithPileFloor(20)

	_, pile, _ := svc.discoveryDue(ctx, sc)
	if err := repo.RecordDiscoveryRun(ctx, sc, pile); err != nil {
		t.Fatal(err)
	}
	// Nothing has arrived in a month. Growth is the trigger for a new subject,
	// but probation verdicts still fall due and the taxonomy still moves.
	backdateDiscovery(t, db, sc, time.Now().UTC().Add(-MaxDiscoveryGap-time.Hour))

	due, _, err := svc.discoveryDue(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatal("a quiet account went indefinitely unreviewed")
	}
}

func backdateDiscovery(t *testing.T, db *store.DB, sc store.Scope, at time.Time) {
	t.Helper()
	if _, err := db.Write.ExecContext(context.Background(),
		`UPDATE user_taxonomy SET discovered_at = ? WHERE user_id = ?`,
		at.Format(time.RFC3339Nano), sc.UserID); err != nil {
		t.Fatal(err)
	}
}

// TestTheCostBudgetGuardsTheRealLimit ties internal/propose's cost gate to the
// constant it is actually protecting.
//
// propose cannot import relabel (relabel imports propose), so its budget test
// hard-codes the corpus size. This is the other half: if DiscoveryCorpusLimit
// moves, the cost gate is silently measuring the wrong number until this fails.
func TestTheCostBudgetGuardsTheRealLimit(t *testing.T) {
	const measured = 2000
	if DiscoveryCorpusLimit != measured {
		t.Fatalf("DiscoveryCorpusLimit is %d but internal/propose's cost gate measures %d.\n"+
			"Clustering is roughly O(n^2.5) and this runs on a 2GB box beside nginx and SQLite: "+
			"measure the new limit, update corpusLimitUnderTest, and confirm the budget still holds.",
			DiscoveryCorpusLimit, measured)
	}
}
