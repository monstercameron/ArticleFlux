//go:build js && wasm

package data

import (
	"context"
	"time"

	"github.com/monstercameron/ArticleFlux/client/design"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/themewire"
)

// The theming calls (§20.16.3).
//
// In this package rather than in smart.go for the same reason smart.go exists at
// all — these are the calls that spend money, and a reader of this package should
// be able to see the whole of that — but in their own file because they are the two
// that a MEMBER can make. Everything in smart.go is owner-only and returns
// PermissionDenied to anybody else, so a caller looking for "what can a normal
// account do that costs something" has one file to read.

// ComposeResult is a generated theme, and what the readability floor had to change
// to get there.
//
// The repairs are not an error. They are what the Appearance screen says out loud:
// a palette that came back a little different from the one described is a thing the
// reader should be told rather than left to suspect. See design.Repair.
type ComposeResult struct {
	Theme   design.Theme
	Repairs []design.Repair
	// Trimmed is true when the prompt was longer than the cap and was cut.
	Trimmed bool
}

// composeTimeout bounds a composition.
//
// Longer than the per-RPC deadline every ordinary call uses, and much shorter than
// a translation's five minutes. A palette is one object of thirteen interdependent
// values from a reasoning model — twenty seconds of legitimate work at the far end
// — and the reader is watching a button, which is the constraint the translator does
// not have.
const composeTimeout = 45 * time.Second

// ComposeTheme turns a phrase into a palette.
func (c *Client) ComposeTheme(parent context.Context, prompt string, tone design.Tone) (
	ComposeResult, error) {

	// Deliberately NOT c.ctx: that applies the short per-RPC deadline, which would
	// abandon a call that is doing exactly what it was asked to do and report it as
	// the server being unreachable.
	ctx, cancel := context.WithTimeout(parent, composeTimeout)
	defer cancel()

	res, err := c.smart.ComposeTheme(ctx, &pb.ComposeThemeRequest{
		Prompt: prompt, Tone: string(tone),
	})
	if err != nil {
		return ComposeResult{}, c.track(err)
	}
	// Validated on arrival. The server has already run the floor, and this runs it
	// again — the client cannot trust the server for the same reason the server
	// cannot trust the client (internal/themewire): the two may be different builds,
	// and the cost of checking is a few dozen float operations against the cost of
	// painting an interface nobody can read.
	theme, err := themewire.Theme(res.GetTheme())
	if err != nil {
		return ComposeResult{}, err
	}
	return ComposeResult{
		Theme:   theme,
		Repairs: themewire.FromRepairs(res.GetRepairs()),
		Trimmed: res.GetPromptTrimmed(),
	}, nil
}

// DriftTarget is where an attuning theme is heading, and why.
//
// Empty — a zero Theme with no Why — is a real and expected answer: it is the cold
// start, a reader whose topics have not formed yet (§18.4). The caller keeps
// whatever target it already had and the screen says there is nothing to attune to.
type DriftTarget struct {
	Target design.Theme
	// Why is the topic label the target was built from, in the reader's own
	// vocabulary. Shown on the Appearance screen: a theme that changes itself and
	// will not say why is a theme nobody trusts (§18.9).
	Why string
	// Signature fingerprints the taste behind this target. Stored, and compared
	// before asking again — which is what keeps a feature that repaints daily from
	// being a request that happens daily.
	Signature string
	// Smart is true when a model wrote this palette rather than the deterministic
	// tint. The screen says which, so "I turned Smart+ off and it still drifts" is
	// explainable rather than alarming.
	Smart   bool
	Repairs []design.Repair
}

// Empty reports the cold start: there is nothing to attune to yet.
func (d DriftTarget) Empty() bool { return d.Signature == "" || d.Target.Ground == "" }

// SuggestTheme asks where the drift should be heading.
//
// The base is the palette currently being painted, and the client sends it because
// the client is the only place that resolves which theme that actually is — the
// reader's chosen theme, or their generated one, already blended however far the
// drift has got. A second resolution on the server would be a second answer.
func (c *Client) SuggestTheme(parent context.Context, base design.Theme) (DriftTarget, error) {
	// The ordinary deadline is right here, unlike ComposeTheme: this call falls back
	// to a deterministic answer the server computes in microseconds, and it runs
	// unattended at boot. Nobody is waiting for it, so nothing is gained by waiting
	// longer for it.
	ctx, cancel := c.ctx(parent)
	defer cancel()

	res, err := c.smart.SuggestTheme(ctx, &pb.SuggestThemeRequest{
		Base: themewire.Tokens(base),
	})
	if err != nil {
		return DriftTarget{}, c.track(err)
	}
	if res.GetTarget() == nil {
		// Cold start. Not an error and not an empty theme: an empty theme has empty
		// colours, and an empty colour applied to documentElement drops the
		// declaration and inherits.
		return DriftTarget{}, nil
	}
	target, err := themewire.Theme(res.GetTarget())
	if err != nil {
		return DriftTarget{}, err
	}
	return DriftTarget{
		Target:    target,
		Why:       res.GetWhy(),
		Signature: res.GetSignature(),
		Smart:     res.GetSmart(),
		Repairs:   themewire.FromRepairs(res.GetRepairs()),
	}, nil
}
