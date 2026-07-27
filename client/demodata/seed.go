package demodata

import (
	"strconv"
	"strings"
	"time"
)

// The sample instance.
//
// Everything below is invented. The publications do not exist, the bylines are
// not anybody's, and every address is under a reserved example domain — which is
// a deliberate choice rather than laziness about finding real feeds. A demo
// seeded with real publishers puts words in their mouths on a page carrying
// somebody else's project name, and the one thing a reader cannot check on a
// static demo is whether the article they are reading was really written.
//
// What the fixtures ARE trying to be is representative, because a demo is an
// argument about the application and an unrepresentative one argues for nothing:
//
//   - Seven sources, because seven is the size of the hand-picked hue palette
//     and the rail's identity colours are most of what it does.
//   - Three categories and three tags, so the difference between them — where a
//     feed LIVES versus what you SAY about it — is visible rather than described.
//   - Articles from twenty minutes to eleven days old, so relative time renders
//     at every scale it has a format for.
//   - A mix of read, unread, starred, rated and annotated, so no screen in the
//     application is empty on arrival.
//   - One feed that has been failing for two days, so the dormant-feed nudge has
//     something to nudge about. A demo where everything works shows a reader
//     nothing about what happens when something does not.

// seedFeed is one subscription in the sample instance.
type seedFeed struct {
	title    string
	host     string
	folder   string
	megafeed bool
	interval int32
	// failH is hours since this feed last fetched successfully, and failures is
	// how many attempts have failed since. Zero for the healthy majority.
	failH     int
	failures  int32
	lastError string
}

var seedFeeds = []seedFeed{
	{title: "Quiet Systems", host: "quietsystems.example", folder: "Technology", megafeed: true, interval: 3600},
	{title: "The Marginal Engineer", host: "marginalengineer.example", folder: "Technology", megafeed: true, interval: 3600},
	{title: "Hackline Daily", host: "hackline.example", folder: "Technology", megafeed: false, interval: 900},
	{title: "Field Notes", host: "fieldnotes.example", folder: "Science", megafeed: true, interval: 7200},
	{title: "Orbital Digest", host: "orbitaldigest.example", folder: "Science", megafeed: true, interval: 3600},
	{title: "The Long Table", host: "longtable.example", folder: "Culture", megafeed: true, interval: 21600},
	{
		title: "Paper & Ink", host: "paperandink.example", folder: "Culture",
		megafeed: true, interval: 21600,
		failH:    50,
		failures: 9,
		// A real error string, phrased the way the fetcher phrases them. The
		// nudge is only useful if it can say what went wrong.
		lastError: "Get \"https://paperandink.example/feed.xml\": dial tcp: lookup paperandink.example: no such host",
	},
}

// seedTags maps a tag to the feeds carrying it, by index into seedFeeds.
//
// A tag is something you SAY about a feed and a category is where it LIVES,
// which is why "longform" crosses Technology and Culture while the categories
// cannot overlap at all. That is the entire argument for having both, and it is
// only visible in data that actually crosses.
var seedTags = []struct {
	name  string
	glyph string
	feeds []int
}{
	{name: "longform", feeds: []int{0, 5, 6}},
	{name: "daily", feeds: []int{2, 4}},
	{name: "keep", feeds: []int{3, 0}},
}

// seedItem is one article. minsAgo is measured from boot, so the demo is always
// current — see the note on New's clock parameter.
type seedItem struct {
	feed    int
	minsAgo int
	title   string
	author  string
	summary string
	body    []string

	read    bool
	starred bool
	rating  int32
	note    string

	// weight is the megafeed's interest score before recency decay, and reason
	// is what the ranked row says about itself. Both are hand-set here because
	// the ranking model they come from lives on a server.
	weight float64
	reason string

	// held keeps an article out of the first load so the refresh button has
	// something to find.
	held bool
}

var seedItems = []seedItem{
	// --- Quiet Systems ---------------------------------------------------------
	{
		feed: 0, minsAgo: 34, weight: 6,
		reason:  "You finish long pieces from Quiet Systems more often than you skip them",
		title:   "The database is not the slow part",
		author:  "Ines Arbeláez",
		summary: "Six months of profiling a request path that everyone was sure was I/O bound, and what was actually there.",
		body: []string{
			"<p>Every team I have worked with has a story about the query that brought the site down, and every one of those stories is true. What is less often true is the conclusion drawn from it, which is that the database is where the time goes. For six months I measured a request path that four separate people had described to me as I/O bound, and the database was responsible for eleven percent of it.</p>",
			"<p>The instinct is not stupid. Storage is the part of the system with the most obvious physical limit, and it is the part that fails loudly. But loud is not the same as slow, and a profiler is not interested in which subsystem has the best story about itself.</p>",
			"<h2>What was actually there</h2>",
			"<p>Serialisation. Not the interesting kind — no compression, no encryption, nothing anyone had chosen. The response object was being built as a map, converted to a struct, converted back to a map by a middleware that wanted to add two fields, and encoded. Three of those four steps existed because somebody needed a header set in 2021.</p>",
			"<blockquote><p>The fastest code is the code that was deleted when the reason for it went away. The second fastest is the code that was never written because someone measured first.</p></blockquote>",
			"<p>I am not going to pretend this generalises into a rule. The point is narrower and, I think, more useful: the subsystem your team talks about most is the subsystem you have already optimised. The time is somewhere nobody is looking, because if anybody were looking it would not still be there.</p>",
		},
	},
	{
		feed: 0, minsAgo: 15 * 60, weight: 5, starred: true,
		reason:  "Starred, and from a source you read to the end",
		title:   "Write the migration before the feature",
		author:  "Ines Arbeláez",
		summary: "A schema change you cannot describe as a migration is a schema change you do not understand yet.",
		body: []string{
			"<p>Here is a habit that has cost me nothing and saved me several weekends: before writing a feature that needs a new column, write the migration that adds it, and then write the one that takes it away again.</p>",
			"<p>Not because you will need the down migration. You almost certainly will not, and a good number of them cannot be written honestly at all — you cannot un-drop a column's contents. The value is in what the exercise refuses to let you avoid.</p>",
			"<ul><li>What happens to rows that already exist?</li><li>What does the old code do when it reads a row the new code wrote?</li><li>What does the new code do when it reads a row the old code wrote?</li></ul>",
			"<p>The third question is the one that gets skipped, and it is the one that matters during a deploy, when both versions are running at once and neither of them has been told which one is supposed to win.</p>",
		},
	},
	{
		feed: 0, minsAgo: 4 * 24 * 60, read: true, weight: 3,
		reason:  "From a category you read most evenings",
		title:   "In praise of the boring queue",
		author:  "Tomás Bergqvist",
		summary: "Everything a job queue needs, and the long list of things it does not.",
		body: []string{
			"<p>A job queue needs to do four things: accept work, hand it to exactly one worker, notice when a worker dies, and let you look at what is waiting. Everything else on the feature list of every queue product I have evaluated is a variation on one of those four or a solution to a problem created by an earlier variation.</p>",
			"<p>We ran a table. One table, with a status column and a claimed_at timestamp, in the database we already had. It has handled every load we have put on it, and the operational burden has been a single query I run when someone asks what the backlog looks like.</p>",
			"<p>I would not tell you it scales forever. I would tell you that the version that does not scale forever shipped in an afternoon, and the version that does was still being configured three weeks later on a different team.</p>",
		},
	},

	// --- The Marginal Engineer -------------------------------------------------
	{
		feed: 1, minsAgo: 2 * 60, weight: 5,
		reason:  "Recent, and on a topic you have opened four times this week",
		title:   "Reading a flamegraph without lying to yourself",
		author:  "Priya Raghunathan",
		summary: "The three misreadings that make a profile look like it says the opposite of what it says.",
		body: []string{
			"<p>A flamegraph is a picture of where the samples landed, and it is very good at looking like a picture of where the time went. Those are the same thing only if you were sampling the right thing, which is the assumption the graph cannot show you.</p>",
			"<h2>Width is not cost</h2>",
			"<p>The widest frame is where the profiler was most often when it looked. If your profiler samples on CPU and your program is waiting on a socket, the frame that is actually costing you a second and a half of wall clock may not appear at all.</p>",
			"<h2>Depth is not blame</h2>",
			"<p>A deep tower is a call chain, not a problem. Recursion draws a skyline. Inlining flattens one. Neither tells you anything about whether the work at the bottom was necessary.</p>",
			"<p>The honest use of a flamegraph is as a way to generate questions quickly. It is a terrible way to answer one, and every time I have skipped the answering step I have optimised something that was not the problem and then had to explain why the graph looked better and the page did not.</p>",
		},
	},
	{
		feed: 1, minsAgo: 26 * 60, read: true, rating: 1, weight: 4,
		reason:  "You liked this",
		title:   "The cost of a dependency is not its size",
		author:  "Priya Raghunathan",
		summary: "Bundle weight is the cheapest thing a library will ever ask of you.",
		body: []string{
			"<p>We measure dependencies in kilobytes because kilobytes are easy to measure. The expensive parts are the ones that do not show up in a bundle report: the API you now have to learn, the upgrade you now have to schedule, the bug you now have to reproduce in somebody else's abstraction before you are allowed to have an opinion about it.</p>",
			"<p>I keep a rough test. If the library disappeared tomorrow, how long would it take to write the part of it we use? If the answer is under a day, we are not buying capability, we are buying a decision we did not want to make — and we will pay interest on that decision every time it needs upgrading.</p>",
		},
	},
	{
		feed: 1, minsAgo: 8 * 24 * 60, read: true, weight: 2,
		title:   "Feature flags are a schema",
		author:  "Marcus Odell",
		summary: "Every flag is a branch in your data model, whether or not you wrote it down as one.",
		body: []string{
			"<p>The pitch for feature flags is that they decouple deploy from release. That is true and it is worth having. What the pitch leaves out is that each flag doubles the number of states your system can be in, and unlike a schema, nobody reviews them.</p>",
			"<p>Three flags is eight configurations. Ten is a thousand and twenty-four, of which you have tested perhaps four. The flags that scare me are not the new ones — they are the ones that have been on for everybody for eighteen months, which nobody has deleted, and which still have a code path underneath them that has not run since.</p>",
		},
	},

	// --- Hackline Daily --------------------------------------------------------
	{
		feed: 2, minsAgo: 22, weight: 2,
		title:   "Standards body publishes revised timeline for the storage API",
		author:  "Hackline Desk",
		summary: "Two of the four contested behaviours are deferred; the origin-scoped quota rules survive largely intact.",
		body: []string{
			"<p>The working group has published a revised timeline, moving two of the four contested behaviours out of the current milestone and leaving the origin-scoped quota rules substantially as drafted.</p>",
			"<p>Implementers had objected that the eviction ordering was unimplementable without exposing information about other origins. The revision sidesteps this by declining to specify an order at all, which several commenters have described, not unfairly, as agreeing to disagree in public.</p>",
		},
	},
	{
		feed: 2, minsAgo: 3 * 60, weight: 1,
		title:   "Compiler release trims build times on large modules",
		author:  "Hackline Desk",
		summary: "Early reports put the improvement between eight and twenty percent, concentrated in projects with deep generic instantiation.",
		body: []string{
			"<p>The point release landed this morning with a change to how generic instantiations are cached across packages. Early reports from projects with deep generic trees put the improvement between eight and twenty percent of wall-clock build time.</p>",
			"<p>Projects without much generic code report no measurable change, which is what the release notes predicted and rather less than the discussion threads did.</p>",
		},
	},
	{
		feed: 2, minsAgo: 9 * 60, read: true,
		title:   "Outage post-mortem points at a health check that was too healthy",
		author:  "Hackline Desk",
		summary: "The check verified the process was running. It did not verify the process could reach anything.",
		body: []string{
			"<p>The published timeline describes a familiar shape: a dependency became unreachable, the affected instances kept passing their health checks, and the load balancer went on sending them traffic for forty minutes.</p>",
			"<p>The check in question returned 200 whenever the HTTP server was accepting connections — which it was, throughout. The remediation is a check that exercises one real dependency, and a note about the difference between liveness and usefulness that is worth more than the incident it came from.</p>",
		},
	},
	{
		feed: 2, minsAgo: 31 * 60, read: true,
		title:   "Package registry adds provenance attestations by default",
		author:  "Hackline Desk",
		summary: "Publishers get signed build provenance without opting in; verification stays opt-in for consumers.",
		body: []string{
			"<p>New publishes now carry build provenance by default. Consumers must still opt in to verifying it, which the registry frames as a migration step and critics frame as the part that would have mattered.</p>",
		},
	},

	// --- Field Notes -----------------------------------------------------------
	{
		feed: 3, minsAgo: 5 * 60, weight: 5, starred: true,
		reason:  "Starred, and the longest thing you read last week was from here",
		title:   "The lichen that keeps time",
		author:  "Dr. Wren Ashcombe",
		summary: "Growth rings in a organism with no rings, and the forty-year measurement that found them.",
		body: []string{
			"<p>Lichens do not have rings. They also do not have a growth season in any sense a temperate-forest botanist would recognise, which is why the claim that you can date a rockfall by the lichen on it has always sat somewhere between folk knowledge and method.</p>",
			"<p>A survey begun in 1984 and finished, by three successive researchers, last spring, has been photographing the same 240 thalli on the same granite outcrop every year. The result is not a clock. It is something more useful: a distribution, with the variance attached, and the variance is where the interesting biology is.</p>",
			"<h2>What the spread says</h2>",
			"<p>Two thalli a metre apart, on the same rock, in the same weather, can differ in radial growth by a factor of three across a decade. The best predictor turned out not to be aspect or shade but the presence of a particular free-living alga in the first two years of establishment — a partner acquired, or not, essentially by chance.</p>",
			"<p>Forty years of photographs to find out that the answer is partly luck. This is, I would argue, the most honest possible outcome for a long-baseline field study, and the least publishable.</p>",
		},
	},
	{
		feed: 3, minsAgo: 2 * 24 * 60, read: true, rating: 1,
		note:    "The bit about the variance being the interesting part — same argument as the profiling piece from Quiet Systems. Worth writing up together.",
		weight:  3,
		title:   "Counting what you cannot catch",
		author:  "Dr. Wren Ashcombe",
		summary: "Mark-recapture, its assumptions, and the three field situations that break every one of them.",
		body: []string{
			"<p>Mark-recapture assumes that marked and unmarked animals are equally likely to be caught the second time. Every field biologist knows this assumption is false. The methodological literature is largely a history of increasingly sophisticated ways to be wrong about how false it is.</p>",
			"<p>The three classic breakages are trap-happiness, trap-shyness, and the population that will not hold still long enough to be a population. The third is the one nobody writes about, because a study that concludes the boundary of your study area was arbitrary is a study that does not get funded twice.</p>",
		},
	},
	{
		feed: 3, minsAgo: 6 * 24 * 60, read: true, weight: 2,
		title:   "A soil sample is a time series",
		author:  "Yusuf Naranjo",
		summary: "What changes between the morning core and the afternoon one, and why it is not noise.",
		body: []string{
			"<p>Two cores from the same square metre, taken six hours apart, will disagree about the microbial community in ways that look like sampling error and are not. Respiration follows temperature closely enough that the afternoon sample is a different community's worth of activity, not a noisier measurement of the morning's.</p>",
			"<p>The practical consequence is dull and important: a protocol that does not fix the time of day has quietly introduced a variable, and it will be correlated with whatever else about the site made you sample it in the afternoon.</p>",
		},
	},

	// --- Orbital Digest --------------------------------------------------------
	{
		feed: 4, minsAgo: 70, weight: 4,
		reason:  "New from a source you check every morning",
		title:   "The launch window that closes for four years",
		author:  "Sana Delacroix",
		summary: "Orbital mechanics does not negotiate, and the schedule pressure that produces is its own kind of risk.",
		body: []string{
			"<p>Most schedule pressure in aerospace is commercial: a contract, a fiscal year, a competitor. Transfer windows are the exception, and they are the only deadline in the industry that cannot be renegotiated by anybody at any price.</p>",
			"<p>Miss this one and the next is in 2030. That fact does useful work — it concentrates attention, it settles arguments about priority — and it does one very dangerous thing, which is that it makes every proposal to slip a test look like a proposal to cancel the mission.</p>",
			"<blockquote><p>A deadline that cannot move is not a discipline. It is a load, and it has to be carried somewhere.</p></blockquote>",
			"<p>The place it usually gets carried is margin. Not schedule margin, which is visible and defended, but the quiet kind: the second look at an anomaly, the test that gets run once instead of three times.</p>",
		},
	},
	{
		feed: 4, minsAgo: 11 * 60, weight: 3,
		title:   "What the ground station heard",
		author:  "Sana Delacroix",
		summary: "Eleven minutes of telemetry, reconstructed from three receivers that each missed a different part.",
		body: []string{
			"<p>No single station had the whole pass. The reconstruction stitches three partial captures, and the interesting work is in the overlaps, where two receivers disagree about the same second of downlink.</p>",
			"<p>Where they disagree, one of them is wrong, and figuring out which is a question about the receivers rather than about the spacecraft — which is the part of this that reads as tedious and is in fact the entire method.</p>",
		},
	},
	{
		feed: 4, minsAgo: 3 * 24 * 60, read: true,
		title:   "Debris tracking has a units problem",
		author:  "Ambrose Tey",
		summary: "Three catalogues, three conventions, and a conjunction warning that meant different things to different operators.",
		body: []string{
			"<p>The warning was accurate in all three catalogues. It was also, in all three, describing a slightly different volume of space, because the covariance conventions differ and the documentation of those differences lives in three PDFs of varying age.</p>",
		},
	},

	// --- The Long Table --------------------------------------------------------
	{
		feed: 5, minsAgo: 7 * 60, weight: 5,
		reason:  "Long, and you finish long ones from this source",
		title:   "The stock pot as an argument",
		author:  "Odile Marchetti",
		summary: "Nothing in a kitchen rewards patience so obviously, and nothing is so consistently skipped.",
		body: []string{
			"<p>You can buy stock. It will be fine. This is not a piece about how it will not be fine, because it will, and I am not interested in the kind of cooking writing that exists to make people feel bad about a Tuesday.</p>",
			"<p>It is a piece about what you learn from making it, which is not a technique. The technique is: put bones in water, keep it below a simmer, wait. What you learn is what a long, low, unattended process feels like when it is going correctly — which is to say, boring, and then suddenly not.</p>",
			"<h2>Six hours in</h2>",
			"<p>There is a moment, and it is not subtle, when the liquid stops smelling of its ingredients and starts smelling of itself. Before that moment you have hot water with things in it. After it you have stock. Nothing you do causes the transition; you are only there for it.</p>",
			"<p>I have come to think this is the whole value of the exercise for people who cook mostly under time pressure. It is a reliable, repeatable experience of a process that cannot be hurried and does not need supervising, in a room where almost everything else is both urgent and interruptible.</p>",
		},
	},
	{
		feed: 5, minsAgo: 2*24*60 + 300, read: true, starred: true,
		note:   "Sent this to Dad. He made the version with the fennel and reported back that it was 'fine, but it's soup'.",
		weight: 4,
		title:   "A bean is a schedule",
		author:  "Odile Marchetti",
		summary: "Soaking, salting, and the two pieces of received wisdom that turn out to be about different beans entirely.",
		body: []string{
			"<p>The advice not to salt beans while they cook, and the advice to salt them heavily from the start, are both correct, and they are describing different beans of different ages under different water.</p>",
			"<p>An old bean has a tougher seed coat and takes longer to hydrate; salt slows that further. A bean from this year's harvest does not care. Nobody selling you dried beans tells you which you have, which is why the reliable instruction is not a rule about salt but a habit of tasting one at forty minutes.</p>",
		},
	},
	{
		feed: 5, minsAgo: 9 * 24 * 60, read: true, rating: -1, weight: 1,
		title:   "In defence of the mediocre restaurant",
		author:  "Felix Anand",
		summary: "The place that is merely adequate does something the exceptional one cannot.",
		body: []string{
			"<p>Every neighbourhood needs somewhere you can go without deciding to. Not a destination, not a discovery, nothing anyone will ask you about afterwards: a room with tables that is open on a Wednesday.</p>",
			"<p>The economics of attention have been very unkind to these places. A restaurant that is fine cannot be written about, and increasingly a restaurant that is not written about cannot survive the rent.</p>",
		},
	},

	// --- Paper & Ink -----------------------------------------------------------
	{
		feed: 6, minsAgo: 3*24*60 + 120, weight: 3,
		reason:  "From a feed that has gone quiet — this is the most recent thing it sent",
		title:   "The second novel problem",
		author:  "Harriet Nkemelu",
		summary: "Everything a debut is allowed to be, and the specific thing the follow-up is not.",
		body: []string{
			"<p>A debut is permitted to be strange in ways a second book is not, and the reason is structural rather than artistic: nobody has expectations of a writer they have not read. The second book is read against the first by every reviewer who liked the first.</p>",
			"<p>The writers who survive this seem to do it by writing the second book before the first is published, which is less a strategy than an accident of timing available to almost nobody.</p>",
		},
	},
	{
		feed: 6, minsAgo: 7 * 24 * 60, read: true, weight: 2,
		title:   "Marginalia as a reading practice",
		author:  "Harriet Nkemelu",
		summary: "Writing in books, the argument against it, and why the argument is about ownership rather than reading.",
		body: []string{
			"<p>The objection to writing in books is almost never about reading. It is about the book as an object that will outlive your reading of it, and about who the next reader is going to be.</p>",
			"<p>Which makes it a question about your library rather than about your comprehension — and comprehension, on the evidence, is helped considerably by a pencil.</p>",
		},
	},
	{
		feed: 6, minsAgo: 11 * 24 * 60, read: true,
		title:   "Translating the untranslatable word, again",
		author:  "Jonas Wirth",
		summary: "Four translators, one paragraph, and four defensible answers.",
		body: []string{
			"<p>Given the same paragraph, four translators produced four versions that agree on nothing except the proper nouns. All four are defensible. Two of them are, to my ear, better than the original.</p>",
		},
	},

	// --- held back, for the refresh button ------------------------------------
	{
		feed: 2, held: true,
		title:   "Runtime ships incremental GC behind a flag",
		author:  "Hackline Desk",
		summary: "Pause times drop by an order of magnitude on the benchmark suite; throughput cost is between two and four percent.",
		body: []string{
			"<p>The flag is off by default and the release notes are unusually direct about why: the throughput cost is real, it is workload-dependent, and the team would rather people measured than trusted a default.</p>",
		},
	},
	{
		feed: 0, held: true, weight: 5,
		reason:  "New, from the source you read most",
		title:   "Backpressure is a conversation",
		author:  "Ines Arbeláez",
		summary: "A queue that only ever grows is a system that has decided not to tell you something.",
		body: []string{
			"<p>An unbounded queue is a way of not having a conversation. The producer never learns that the consumer is struggling; it learns, eventually, that the process died.</p>",
			"<p>Every bounded queue forces a decision you would rather not make — block, drop, or fail — and that decision is the design. Choosing not to bound it is not avoiding the decision. It is choosing 'fail', later, at a time you will not pick.</p>",
		},
	},
	{
		feed: 4, held: true, weight: 3,
		title:   "A second look at the anomaly on approach",
		author:  "Sana Delacroix",
		summary: "The revised reconstruction moves the event eleven seconds earlier, which changes what could have caused it.",
		body: []string{
			"<p>Eleven seconds does not sound like a revision worth publishing. It moves the event to before the burn rather than during it, which removes the two most-discussed explanations and leaves one nobody has liked so far.</p>",
		},
	},
	{
		feed: 3, held: true,
		title:   "Field season notes: what the rain did",
		author:  "Yusuf Naranjo",
		summary: "Six weeks of unusual rainfall, and a transect that has to be re-walked before any of it means anything.",
		body: []string{
			"<p>The transect was laid out for a dry year. It has not been a dry year, and the honest report is that half the plots are measuring something other than what they were placed to measure.</p>",
		},
	},
}

// seed builds the sample instance.
func seed(now func() time.Time) *Instance {
	if now == nil {
		now = time.Now
	}
	t := now()
	in := &Instance{
		now:     now,
		started: t,
		prefs:   map[string]string{},
		undo:    map[string][]string{},
		applied: map[string]bool{},
		calls:   map[string]int64{},
	}

	// Categories, in the order the fixtures mention them, so the rail order is
	// the order somebody would have created them in.
	folderIDs := map[string]string{}
	for _, f := range seedFeeds {
		if f.folder == "" || folderIDs[f.folder] != "" {
			continue
		}
		fo := &folder{id: in.nextID("fold"), name: f.folder, position: int32(len(in.folders))}
		in.folders = append(in.folders, fo)
		folderIDs[f.folder] = fo.id
	}

	for _, sf := range seedFeeds {
		f := &feed{
			id:            in.nextID("sub"),
			sourceID:      in.nextID("src"),
			pubTitle:      sf.title,
			feedURL:       "https://" + sf.host + "/feed.xml",
			siteURL:       "https://" + sf.host + "/",
			folderID:      folderIDs[sf.folder],
			kind:          "feed",
			inMegafeed:    sf.megafeed,
			fetchInterval: sf.interval,
			failures:      sf.failures,
			lastError:     sf.lastError,
			lastFetch:     t.Add(-20 * time.Minute),
		}
		if sf.failH > 0 {
			f.lastSuccess = t.Add(-time.Duration(sf.failH) * time.Hour)
		} else {
			f.lastSuccess = t.Add(-20 * time.Minute)
		}
		in.feeds = append(in.feeds, f)
	}

	for _, st := range seedItems {
		it := &item{
			id:      in.nextID("item"),
			feed:    in.feeds[st.feed],
			title:   st.title,
			author:  st.author,
			summary: st.summary,
			body:    strings.Join(st.body, "\n"),
			read:    st.read,
			starred: st.starred,
			rating:  st.rating,
			note:    st.note,
			weight:  st.weight,
			reason:  st.reason,
		}
		it.url = "https://" + seedFeeds[st.feed].host + "/" + slug(st.title)
		it.words = wordCount(st.body)
		it.published = t.Add(-time.Duration(st.minsAgo) * time.Minute)
		if it.read {
			it.readAt = stamp(t.Add(-time.Duration(st.minsAgo/2) * time.Minute))
		}
		if it.note != "" {
			it.noteAt = t.Add(-time.Duration(st.minsAgo/3) * time.Minute)
		}
		if st.held {
			in.incoming = append(in.incoming, it)
			continue
		}
		in.items = append(in.items, it)
	}
	in.resort()

	for _, stg := range seedTags {
		tg := &tag{
			id: in.nextID("tag"), name: stg.name, glyph: stg.glyph,
			sources: map[string]bool{},
		}
		for _, idx := range stg.feeds {
			tg.sources[in.feeds[idx].sourceID] = true
		}
		in.tags = append(in.tags, tg)
	}

	// A little history, so the activity screen is not empty on arrival. The
	// stamps are the instance's own clock, so they read as "just now" the way
	// everything else in the demo does.
	in.logf("INFO", "demo instance started", "build", Version)
	in.logf("INFO", "polled every source", "feeds", strconv.Itoa(len(in.feeds)),
		"items", strconv.Itoa(len(in.items)))
	if f := in.feeds[len(in.feeds)-1]; f.failures > 0 {
		in.logf("ERROR", "fetch failed", "source", f.title(), "error", f.lastError)
	}
	return in
}

// --- feeds added while the demo is running -------------------------------------

// addFeed creates a subscription for an address nobody has seen before.
// Caller holds the lock.
func (in *Instance) addFeed(url, title, folderID, kind string) *feed {
	host := hostOf(url)
	if title == "" {
		title = titleFromHost(host)
	}
	if folderID != "" && in.folderByID(folderID) == nil {
		folderID = ""
	}
	f := &feed{
		id:            in.nextID("sub"),
		sourceID:      in.nextID("src"),
		pubTitle:      title,
		feedURL:       "https://" + host + "/feed.xml",
		siteURL:       "https://" + host + "/",
		folderID:      folderID,
		kind:          kind,
		inMegafeed:    true,
		fetchInterval: 3600,
		lastFetch:     in.clock(),
		lastSuccess:   in.clock(),
	}
	in.feeds = append(in.feeds, f)
	in.logf("INFO", "subscribed", "source", f.title(), "url", f.feedURL)
	return f
}

// stockItems gives a newly added feed something to show. Caller holds the lock.
//
// The articles are obviously generic, and that is the point: a demo that
// invented plausible-looking content for a domain somebody typed would be
// putting words under a real publisher's name. These say what they are.
func (in *Instance) stockItems(f *feed, n int) int32 {
	titles := []string{
		"A first post, and what this feed is for",
		"Notes from the week",
		"Three things worth reading",
		"On starting again",
		"A short update",
	}
	now := in.clock()
	var made int32
	for i := 0; i < n && i < len(titles); i++ {
		it := &item{
			id:        in.nextID("item"),
			feed:      f,
			title:     titles[i],
			author:    f.title(),
			summary:   "Sample text. The demo has no server, so nothing was fetched from " + hostOf(f.siteURL) + ".",
			url:       f.siteURL + slug(titles[i]),
			published: now.Add(-time.Duration(i*7+1) * time.Hour),
			words:     120,
			weight:    2,
			body: "<p>This article was generated by the ArticleFlux demo when you subscribed to " +
				htmlEscape(f.title()) + ".</p>" +
				"<p>A real instance would have fetched the feed, parsed it, extracted the " +
				"full text of each entry and stored it. The demo runs entirely in this tab " +
				"and cannot reach the network, so it made this up instead — visibly, rather " +
				"than convincingly.</p>",
		}
		in.items = append(in.items, it)
		made++
	}
	return made
}

// slug is the article path in a fabricated URL. Nothing resolves it; it exists
// so the addresses on screen look like addresses.
func slug(title string) string {
	var b strings.Builder
	prev := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prev = false
		case !prev && b.Len() > 0:
			b.WriteByte('-')
			prev = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// wordCount is what the reading-time estimate is built on, counted off the
// markup rather than guessed: the client shows "12 min" from this number, and a
// made-up count would make the one number on the row that claims precision the
// only one that is wrong.
func wordCount(paras []string) int32 {
	var n int32
	for _, p := range paras {
		inTag := false
		inWord := false
		for _, r := range p {
			switch {
			case r == '<':
				inTag = true
				inWord = false
			case r == '>':
				inTag = false
			case inTag:
			case r == ' ' || r == '\n' || r == '\t':
				inWord = false
			default:
				if !inWord {
					n++
					inWord = true
				}
			}
		}
	}
	return n
}

// htmlEscape is the minimum needed to put a feed title someone typed inside
// generated markup.
//
// The server sanitises everything it ingests, and this is the demo's one place
// that builds HTML from an input — a title straight off the add-a-feed form. It
// is a small enough surface to handle here rather than pull in a package for,
// and leaving it unescaped would be demonstrating the bug the sanitiser exists
// to prevent.
func htmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;",
	).Replace(s)
}
