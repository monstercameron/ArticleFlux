// Package httpx holds the server's HTTP surface. For now that is one page.
//
// A26 note: every rule below is authored in Go through the GWC css package and
// serialised with css.StyleBlock() — the same author-in-Go / ship-inline pipeline
// the wasm client will use, exercised natively. There is no .css file and no
// application JavaScript on this page, including no refresh script: the poll is a
// <meta http-equiv="refresh">, which is HTML.
//
// Doing this at Tier 1 rather than Tier 8 is deliberate. "The css package can
// express the design" was a desk finding, and D2 is a fresh reminder of what desk
// findings are worth.
package httpx

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/internal/buildstatus"
)

// Build identifies the running binary. Set from main.
type Build struct {
	Version string
	Commit  string
	Addr    string
}

// BootPage serves the status page described in design/README.md's terms: the
// build rendered as a feed, because a feed row is what this application is made
// of. Completed tiers render read, pending tiers render unread — so progress is
// carried by the same weight-and-hue treatment the reader itself uses, and the
// page needs no progress bar.
type BootPage struct {
	Build    Build
	TODOPath string

	styleOnce sync.Once
	style     string
}

// ServeHTTP renders the page fresh on every request. It is a dev instrument on
// localhost; correctness beats caching, and a cached build-status page is a
// build-status page that lies.
func (p *BootPage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	st := buildstatus.Read(p.TODOPath)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(p.render(st)))
}

func (p *BootPage) render(st buildstatus.Status) string {
	p.styleOnce.Do(func() { p.style = p.sheet() })

	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head>`)
	b.WriteString(`<meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	// Ten seconds: fast enough to watch a build land, slow enough not to yank the
	// page out from under someone reading it.
	b.WriteString(`<meta http-equiv="refresh" content="10">`)
	b.WriteString(`<title>ArticleFlux — build status</title>`)
	// Progressive enhancement only. The stacks in client/design are chosen so an
	// offline box degrades to something in the same family, and this page must
	// work on a machine with no network — that is half of what it is proving.
	b.WriteString(`<link rel="preconnect" href="https://fonts.googleapis.com">`)
	b.WriteString(`<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>`)
	b.WriteString(`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Fraunces:opsz,wght,SOFT,WONK@9..144,300..700,0..100,0..1&family=Literata:opsz,wght@7..72,300..600&family=Outfit:wght@300..600&display=swap">`)
	b.WriteString(p.style)
	b.WriteString(`</head><body>`)

	b.WriteString(`<a class="skip" href="#build">Skip to build status</a>`)

	// Always-visible connection state. A reader that has silently stopped
	// receiving looks identical to a quiet news day — the rule applies to the
	// server's own status line first.
	pending := st.Pending()
	b.WriteString(`<header class="bar">`)
	b.WriteString(`<span class="mark">ArticleFlux</span>`)
	b.WriteString(`<span class="state"><i class="dot" aria-hidden="true"></i>server up · client not built</span>`)
	if st.Err == nil {
		b.WriteString(`<span class="count"><b>` + strconv.Itoa(pending) + `</b> tiers to go</span>`)
	}
	b.WriteString(`</header>`)

	b.WriteString(`<main>`)

	b.WriteString(`<section class="lede">`)
	b.WriteString(`<h1>The reader isn't built yet.</h1>`)
	b.WriteString(`<p>This is the server, and it is the whole application so far. ` +
		`It re-reads <code>TODO.md</code> on every request, so the list below is the build as it ` +
		`actually stands rather than a snapshot someone remembered to update.</p>`)
	b.WriteString(`</section>`)

	b.WriteString(`<section id="build">`)
	b.WriteString(`<h2 class="eyebrow">Build</h2>`)

	switch {
	case st.Err != nil:
		// An error state gets direction, not mood.
		b.WriteString(`<p class="problem">Couldn't read <code>` + html.EscapeString(p.TODOPath) +
			`</code>: ` + html.EscapeString(st.Err.Error()) +
			`. Start the server from the repo root, or pass <code>-todo</code>.</p>`)
	case len(st.Tiers) == 0:
		b.WriteString(`<p class="problem">No <code>## Tier</code> sections found in ` +
			html.EscapeString(p.TODOPath) + `. Nothing to report yet.</p>`)
	default:
		b.WriteString(`<ol class="feed">`)
		for _, t := range st.Tiers {
			cls := "row unread"
			if t.Complete() {
				cls = "row read"
			}
			// The hue is the tier's identity and stays at full strength whether or
			// not the tier is finished. Read/unread is a state, and a state must
			// not overwrite an identity — a read article still needs to say who
			// wrote it. Recession is carried by weight and background instead.
			b.WriteString(`<li class="` + cls + `" style="--c:` + design.HueFor(t.ID) + `">`)
			b.WriteString(`<span class="tier">` + html.EscapeString(t.Label) + `</span>`)
			b.WriteString(`<span class="name">` + html.EscapeString(t.Name) + `</span>`)
			b.WriteString(`<span class="tally">` + strconv.Itoa(t.Done) + `<i>/</i>` +
				strconv.Itoa(t.Total) + `</span>`)
			b.WriteString(`</li>`)
		}
		b.WriteString(`</ol>`)
	}
	b.WriteString(`</section>`)

	if len(st.Gates) > 0 {
		b.WriteString(`<section id="gates">`)
		b.WriteString(`<h2 class="eyebrow">Gates</h2>`)
		b.WriteString(`<p class="note">The questions the plan admits it can't answer at a desk. ` +
			`Each one has to be run.</p>`)
		b.WriteString(`<ul class="gates">`)
		for _, g := range st.Gates {
			state, mark := "open", "·"
			if g.Passed {
				state, mark = "passed", "✓"
			}
			b.WriteString(`<li class="gate ` + state + `">`)
			b.WriteString(`<span class="gmark" aria-hidden="true">` + mark + `</span>`)
			b.WriteString(`<span class="gid">` + html.EscapeString(g.ID) + `</span>`)
			b.WriteString(`<span class="gq">` + inlineMD(g.Question) + `</span>`)
			b.WriteString(`<span class="gstate">` + state + `</span>`)
			b.WriteString(`</li>`)
		}
		b.WriteString(`</ul></section>`)
	}

	b.WriteString(`</main>`)

	b.WriteString(`<footer>`)
	b.WriteString(html.EscapeString(p.Build.Version) + ` · ` + html.EscapeString(p.Build.Commit) +
		` · ` + html.EscapeString(p.Build.Addr) +
		` · read at ` + time.Now().Format("15:04:05"))
	b.WriteString(`</footer>`)

	b.WriteString(`</body></html>`)
	return b.String()
}

// inlineMD renders the two inline markdown spans that actually occur in the
// source docs — `code` and *emphasis* — and escapes everything else.
//
// It is not a markdown parser and must not become one. Its job is that a gate
// question copied out of TODO.md reads as a sentence rather than as source: the
// question "Does `ncruces/go-sqlite3` FTS5 work in *our* build?" is the one place
// on this page where text arrives already marked up.
//
// Escaping happens per segment, so no marked-up span can smuggle in a tag.
func inlineMD(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexAny(s, "`*")
		if i < 0 {
			b.WriteString(html.EscapeString(s))
			return b.String()
		}
		delim := s[i]
		j := strings.IndexByte(s[i+1:], delim)
		if j < 0 {
			// Unpaired marker: it is literal text, not a broken tag.
			b.WriteString(html.EscapeString(s[:i+1]))
			s = s[i+1:]
			continue
		}
		b.WriteString(html.EscapeString(s[:i]))
		inner := html.EscapeString(s[i+1 : i+1+j])
		if delim == '`' {
			b.WriteString("<code>" + inner + "</code>")
		} else {
			b.WriteString("<em>" + inner + "</em>")
		}
		s = s[i+j+2:]
	}
}

// sheet authors the whole page in Go and harvests it as an inline <style>.
//
// Mobile-first: the base rules are the phone layout and the media queries add
// width, so a narrow viewport is the case that cannot be forgotten.
func (p *BootPage) sheet() string {
	r := css.Raw

	css.Preflight()

	css.Root(
		css.Custom("ground", design.Ground),
		css.Custom("raised", design.Raised),
		css.Custom("sunk", design.Sunk),
		css.Custom("cream", design.Cream),
		css.Custom("dim", design.Dim),
		css.Custom("line", design.Line),
		css.Custom("display", design.Display),
		css.Custom("reading", design.Reading),
		css.Custom("ui", design.UI),
		r("color-scheme", "dark"),
	)

	css.Global("body",
		r("margin", "0"),
		r("background", "var(--ground)"),
		r("color", "var(--cream)"),
		r("font-family", "var(--ui)"),
		r("font-size", "16px"),
		r("line-height", "1.5"),
		r("-webkit-font-smoothing", "antialiased"),
		// A single wash off the top-left, the same gesture that sits behind an
		// article in the reader. One atmospheric element, not a gradient scheme.
		r("background-image",
			"radial-gradient(120vw 80vh at 8% -10%, #2E2340 0%, transparent 60%)"),
		r("background-attachment", "fixed"),
		r("min-height", "100vh"),
	)

	css.Global(".skip",
		r("position", "absolute"), r("left", "-9999px"),
		r("top", "0.5rem"), r("padding", "0.5rem 0.9rem"),
		r("background", "var(--cream)"), r("color", "var(--ground)"),
		r("border-radius", "0.4rem"), r("font-weight", "600"),
		r("z-index", "10"), r("text-decoration", "none"),
	)
	css.Global(".skip:focus", r("left", "0.75rem"))

	// --- status bar -----------------------------------------------------------
	css.Global(".bar",
		r("display", "flex"), r("flex-wrap", "wrap"),
		r("align-items", "baseline"), r("gap", "0.5rem 1rem"),
		r("padding", "0.9rem 1.1rem"),
		r("background", "color-mix(in oklab, var(--sunk) 82%, transparent)"),
		r("backdrop-filter", "blur(8px)"),
		r("border-bottom", "1px solid var(--line)"),
		r("position", "sticky"), r("top", "0"), r("z-index", "5"),
	)
	css.Global(".mark",
		r("font-family", "var(--display)"),
		r("font-variation-settings", `"SOFT" 60, "WONK" 1, "opsz" 40`),
		r("font-weight", "600"), r("font-size", "1.35rem"),
		r("letter-spacing", "-0.01em"),
	)
	css.Global(".state",
		r("display", "inline-flex"), r("align-items", "center"), r("gap", "0.45rem"),
		r("color", "var(--dim)"), r("font-size", "0.82rem"),
	)
	// The only motion on the page, and it carries information: it says the page
	// you are looking at is live. Everything else holds still — the page reloads
	// every ten seconds, and anything that animated on each load would be noise.
	css.Global(".dot",
		r("width", "0.5rem"), r("height", "0.5rem"), r("border-radius", "50%"),
		r("background", "#7BD88F"), r("flex", "0 0 auto"),
		css.Keyframes("pulse",
			css.At("0%", r("box-shadow", "0 0 0 0 rgba(123,216,143,0.5)")),
			css.At("70%", r("box-shadow", "0 0 0 0.5rem rgba(123,216,143,0)")),
			css.At("100%", r("box-shadow", "0 0 0 0 rgba(123,216,143,0)")),
		),
		r("animation-duration", "2.4s"),
		r("animation-timing-function", "ease-out"),
		r("animation-iteration-count", "infinite"),
	)
	css.Global(".count",
		r("margin-left", "auto"), r("color", "var(--dim)"), r("font-size", "0.82rem"),
	)
	css.Global(".count b",
		r("font-family", "var(--display)"), r("font-size", "1.1rem"),
		r("color", "var(--cream)"), r("font-weight", "600"),
		r("margin-right", "0.15rem"),
	)

	// --- page ------------------------------------------------------------------
	css.Global("main",
		r("max-width", "46rem"), r("margin", "0 auto"),
		r("padding", "2.2rem 1.1rem 3rem"),
	)
	css.Global(".lede h1",
		r("font-family", "var(--display)"),
		r("font-variation-settings", `"SOFT" 80, "WONK" 1, "opsz" 96`),
		r("font-weight", "500"),
		r("font-size", "clamp(1.9rem, 6vw, 3rem)"),
		r("line-height", "1.08"), r("letter-spacing", "-0.02em"),
		r("margin", "0 0 0.75rem"),
	)
	css.Global(".lede p",
		r("font-family", "var(--reading)"), r("color", "var(--dim)"),
		r("font-size", "1rem"), r("max-width", "34rem"), r("margin", "0"),
	)
	css.Global("code",
		r("font-family", `ui-monospace, "Cascadia Code", Consolas, monospace`),
		r("font-size", "0.88em"), r("color", "var(--cream)"),
	)
	css.Global(".eyebrow",
		r("font-size", "0.7rem"), r("text-transform", "uppercase"),
		r("letter-spacing", "0.16em"), r("color", "var(--dim)"),
		r("font-weight", "500"), r("margin", "2.6rem 0 0.8rem"),
	)
	css.Global(".note",
		r("font-family", "var(--reading)"), r("color", "var(--dim)"),
		r("font-size", "0.9rem"), r("margin", "-0.4rem 0 0.9rem"),
	)
	css.Global(".problem",
		r("font-family", "var(--reading)"),
		r("background", "color-mix(in oklab, #E0796B 14%, var(--raised))"),
		r("border-left", "3px solid #E0796B"),
		r("padding", "0.85rem 1rem"), r("border-radius", "0 0.4rem 0.4rem 0"),
		r("margin", "0"),
	)

	// --- the feed: this page's one idea ---------------------------------------
	// Each tier is a row of the reader, carrying its own hue, read when finished
	// and unread when not. That single reuse is the whole design — no bars, no
	// percentages, no rings.
	css.Global(".feed",
		r("list-style", "none"), r("margin", "0"), r("padding", "0"),
		r("display", "grid"), r("gap", "0.35rem"),
	)
	css.Global(".row",
		r("display", "grid"),
		r("grid-template-columns", "1fr auto"),
		r("grid-template-areas", `"tier tally" "name name"`),
		r("gap", "0.1rem 0.6rem"),
		r("align-items", "baseline"),
		r("padding", "0.7rem 0.9rem"),
		r("border-left", "3px solid var(--c)"),
		r("background", "var(--raised)"),
		r("border-radius", "0 0.4rem 0.4rem 0"),
	)
	css.Global(".row .tier",
		r("grid-area", "tier"),
		r("font-size", "0.7rem"), r("letter-spacing", "0.14em"),
		r("text-transform", "uppercase"), r("color", "var(--c)"),
	)
	css.Global(".row .name",
		r("grid-area", "name"),
		r("font-family", "var(--reading)"), r("font-size", "1.02rem"),
	)
	css.Global(".row .tally",
		r("grid-area", "tally"),
		r("font-variant-numeric", "tabular-nums"),
		r("font-size", "0.85rem"), r("color", "var(--dim)"),
	)
	css.Global(".row .tally i",
		r("font-style", "normal"), r("opacity", "0.45"), r("margin", "0 0.15rem"),
	)
	// Unread: full weight, the state that asks for attention.
	css.Global(".row.unread .name", r("font-weight", "500"), r("color", "var(--cream)"))
	// Read: the row recedes but does not vanish — you can still see it was there,
	// which is the point of a read row. The hue edge dims rather than desaturating,
	// so the tier stays identifiable at a glance.
	css.Global(".row.read",
		r("background", "transparent"),
		r("border-left-color", "color-mix(in oklab, var(--c) 55%, transparent)"),
	)
	css.Global(".row.read .name", r("color", "var(--dim)"), r("font-weight", "300"))
	css.Global(".row.read .tally", r("opacity", "0.55"))

	// --- gates -----------------------------------------------------------------
	css.Global(".gates",
		r("list-style", "none"), r("margin", "0"), r("padding", "0"),
		r("display", "grid"), r("gap", "0.15rem"),
	)
	css.Global(".gate",
		r("display", "grid"),
		r("grid-template-columns", "1.1rem 2rem 1fr"),
		r("grid-template-areas", `"gmark gid gq" ". . gstate"`),
		r("gap", "0 0.5rem"),
		r("padding", "0.5rem 0"),
		r("border-bottom", "1px solid var(--line)"),
		r("font-size", "0.88rem"),
	)
	css.Global(".gate .gmark", r("grid-area", "gmark"), r("color", "var(--dim)"))
	css.Global(".gate .gid",
		r("grid-area", "gid"), r("font-weight", "600"), r("color", "var(--cream)"),
	)
	css.Global(".gate .gq",
		r("grid-area", "gq"), r("font-family", "var(--reading)"), r("color", "var(--dim)"),
	)
	css.Global(".gate .gstate",
		r("grid-area", "gstate"),
		r("font-size", "0.72rem"), r("letter-spacing", "0.12em"),
		r("text-transform", "uppercase"), r("color", "var(--dim)"),
	)
	css.Global(".gate.passed .gmark", r("color", "#7BD88F"))
	css.Global(".gate.passed .gstate", r("color", "#7BD88F"))

	css.Global("footer",
		r("max-width", "46rem"), r("margin", "0 auto"),
		r("padding", "0 1.1rem 2.5rem"),
		r("color", "var(--dim)"), r("font-size", "0.76rem"),
	)

	// --- wider than a phone -----------------------------------------------------
	// 640px is where a tier row stops needing two lines. The rows go single-line
	// and the gate state moves up beside its question.
	css.Global(".row", css.Media(css.MinW(640),
		r("grid-template-columns", "6rem 1fr auto"),
		r("grid-template-areas", `"tier name tally"`),
		r("padding", "0.75rem 1rem"),
		r("align-items", "center"),
	)...)
	css.Global(".gate", css.Media(css.MinW(640),
		r("grid-template-columns", "1.1rem 2rem 1fr auto"),
		r("grid-template-areas", `"gmark gid gq gstate"`),
		r("align-items", "baseline"),
	)...)
	css.Global("main", css.Media(css.MinW(640),
		r("padding", "3rem 1.5rem 3.5rem"),
	)...)
	css.Global(".bar", css.Media(css.MinW(640),
		r("padding", "1rem 1.5rem"),
	)...)
	css.Global("footer", css.Media(css.MinW(640),
		r("padding", "0 1.5rem 3rem"),
	)...)

	css.Global(".dot", css.Media(css.RawMedia("(prefers-reduced-motion: reduce)"),
		r("animation", "none"),
	)...)

	return css.StyleBlock()
}

// Handler wires the page plus the two endpoints that answer "is it up" without
// rendering anything.
func (p *BootPage) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", p)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "%s %s\n", p.Build.Version, p.Build.Commit)
	})
	return mux
}
