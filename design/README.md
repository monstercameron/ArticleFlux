# design/ — specifications, not source

These files are **hand-written CSS and vanilla JavaScript on purpose**, because that is the fastest
way to decide how something should look. **Nobody ports them.** They get reimplemented in Go against
`plan.md` §20.6 (A26): all logic and all CSS in GWC, no `.css` files, no application JS,
`syscall/js` quarantined in `client/platform`.

Read the mockups as a spec for *behaviour and appearance*, never as an implementation to copy.

| File | Status | What it is |
|---|---|---|
| `index.html` | — | Compare the directions side by side |
| `01-modern.html` | reference only | Rejected direction, kept for contrast |
| `02-utilitarian.html` | reference only | Rejected direction, kept for contrast |
| **`03-fanciful.html`** | **chosen — desktop** | Three panes, resizable, responsive down to a drawer |
| **`04-fanciful-mobile.html`** | **chosen — phone** | Fills a phone; renders in a device frame on desktop |

## The one real idea

**Every source owns a hue**, and that hue runs through its sidebar dot, its list-row edge, the tint
under its ranking reason, and the radial wash behind its article. On a phone especially, a 3px
coloured edge answers "who wrote this" faster than reading a byline.

In Go this becomes `HueFor(sourceID)` in `client/design/tokens.go` — a **pure function**, so all four
surfaces always agree. (TODO 8.5.)

## The reader is built. `http://127.0.0.1:9000`

Three panes, real feeds, the Google Reader key map. Built from these mockups, not ported from them:
the layout lives in `client/design/sheet.go`, authored entirely through the GWC `css` package.

**The hue system is the thing to check first, because it is the thing that silently broke.**
`html.Props{Style: {"--c": …}}` looks correct and does nothing — GWC's adapter sets styles via JS
property assignment, and that cannot set CSS custom properties. Everything rendered grey. The fix is a
style *string* through `Raw`, which takes the `setAttribute` path instead. It was caught by
screenshotting the built app against these files, which is the argument for doing that at all.

One rule the build corrected: **state must not overwrite identity.** A read row keeps its hue at full
strength and dims only the edge's alpha. Desaturating it made the row recede correctly and lose which
feed it came from — and a read article still needs to say who wrote it.

Parity is checked mechanically in `e2e/design-parity.spec.mjs`: the palette, the three type stacks,
and that one hue reaches all four surfaces (dot, row edge, dateline, article wash) for the same
source. Composition is checked by eye, against paired screenshots the same spec writes into
`e2e/shots/`.

## Verified: GWC can express all of it

Checked against the `css` package, so no part of the design needs an escape to a stylesheet. Rows
marked ✅ have since been **executed natively** on the boot page and their emitted output inspected —
the rest are still desk findings, which D2 is a reminder to treat as provisional.

| Design element | GWC |
|---|---|
| Per-source hue as `--c` | `css.Custom` + `css.Var` ✅ |
| Radial wash behind the article | `css.RadialGradient` |
| Tint under a ranking reason | `css.ColorMix` — **emits `in oklab`, not `in srgb`**, so tints shift slightly from these mockups |
| Fraunces `SOFT`/`WONK` axes, `backdrop-filter`, `-webkit-line-clamp`, `env(safe-area-inset-*)` | `css.Raw(prop, value)` ✅ |
| Breakpoints | `css.Media` ✅ |
| `:root` tokens, reset, `@font-face` | `css.Root`, `css.Global`, `css.Preflight` ✅ |
| Transitions | `css.Keyframes` / `css.At` ✅ — the supported route. It emits a **content-hashed** animation name (`pulse-2s5enck…`) and sets `animation-name` on the rule, so pair it with `animation-duration`/`-iteration-count` rather than the `animation` shorthand |

## Constraints the mockups already honour

Carried from `plan.md`, so the design is buildable rather than aspirational:

- **Fixed-height rows** — `html.VirtualList` cannot do variable heights, so each density mode has its
  own fixed height (96px here). Settle the height before designing the row.
- **Always-visible connection state** — a reader that has silently stopped receiving looks identical
  to a quiet news day.
- **An explainability line on every ranked item** — the reason is what makes feedback actionable.
- **The dormant-feed nudge** — a reader that can't tell you a feed died isn't finished.
- **Google Reader vocabulary intact** — Unread, Starred, and the `j`/`k`/`o`/`s`/`m` keys. Muscle
  memory transfers on day one; cute renaming throws that away for nothing.

## What went wrong the first time, so it doesn't again

Two earlier directions took a metaphor — a post room, an accounting ledger — and dressed the UI in it.
The postmark encoded source initials and hours-ago, which the dateline already said. The green-bar
bands were printout cosplay. Both were rejected, correctly.

**Ornament must carry information.** The per-source hue survives because it tells you something no
label does; a postmark did not.
