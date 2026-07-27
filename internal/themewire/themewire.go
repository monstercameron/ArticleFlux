// Package themewire converts a design.Theme to and from its proto message.
//
// # Why this is its own package
//
// Both ends need the conversion. The server builds a ThemeTokens when it has
// generated a palette; the client builds one when it tells the server which
// palette it is drifting from, and reads one back. Written twice — once in
// internal/transport/grpcsrv and once in client/data — the two copies agree until
// somebody adds a token, and then the symptom is one field silently arriving as
// the empty string, which for a colour means the CSS declaration is dropped and
// the element inherits. That is a defect with no error and no visible cause.
//
// It cannot live in client/design, which holds the Theme: that package is
// deliberately dependency-free so both surfaces can import it, and having the
// palette know about a transport would invert the direction the rest of the
// codebase points in.
package themewire

import (
	"github.com/monstercameron/ArticleFlux/client/design"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Tokens puts a Theme on the wire.
//
// Field by field rather than through a loop over design.Theme.Vars(), even though
// Vars() is the engine's single ordered list and looping would be shorter. The
// proto message has NAMED fields, and matching names to list positions is a
// mapping that keeps compiling after a token is inserted in the middle — it just
// starts sending --hair as --line. An explicit assignment fails to compile when a
// field is renamed and is caught by TestTheMappingIsLossless when one is added.
func Tokens(t design.Theme) *pb.ThemeTokens {
	return &pb.ThemeTokens{
		Name: t.Name, Label: t.Label, Blurb: t.Blurb, Tone: string(t.Tone),
		Ground: t.Ground, Raised: t.Raised, Sunk: t.Sunk,
		Line: t.Line, Hair: t.Hair,
		Cream: t.Cream, Soft: t.Soft, Dim: t.Dim, Read: t.Read,
		Accent: t.Accent, Pos: t.Pos, Neg: t.Neg,
		Shadow: t.Shadow, Wash: t.Wash,
	}
}

// Theme reads a Theme off the wire, and validates it.
//
// Through design.Sanitize, so a palette crossing in either direction is checked
// before it is used: the server cannot trust a client, the client cannot trust a
// stored preference (design.DecodeTheme), and neither can trust a provider. Each
// check is a few dozen float operations and the failure it prevents is an interface
// that cannot be read.
//
// A nil message is design.ErrNotAPalette rather than a zero Theme, because a zero
// Theme has empty colours and empty colours paint nothing — an absent palette must
// not be mistakable for a black one.
func Theme(in *pb.ThemeTokens) (design.Theme, error) {
	if in == nil {
		return design.Theme{}, design.ErrNotAPalette
	}
	t := design.Theme{
		Name: in.GetName(), Label: in.GetLabel(), Blurb: in.GetBlurb(),
		Tone:   design.Tone(in.GetTone()),
		Ground: in.GetGround(), Raised: in.GetRaised(), Sunk: in.GetSunk(),
		Line: in.GetLine(), Hair: in.GetHair(),
		Cream: in.GetCream(), Soft: in.GetSoft(), Dim: in.GetDim(), Read: in.GetRead(),
		Accent: in.GetAccent(), Pos: in.GetPos(), Neg: in.GetNeg(),
		Shadow: in.GetShadow(), Wash: in.GetWash(),
	}
	out, _, err := design.Sanitize(t)
	return out, err
}

// Repairs puts the readability floor's changes on the wire.
func Repairs(reps []design.Repair) []*pb.ThemeRepair {
	if len(reps) == 0 {
		return nil
	}
	out := make([]*pb.ThemeRepair, 0, len(reps))
	for _, r := range reps {
		out = append(out, &pb.ThemeRepair{
			Token: r.Token, From: r.From, To: r.To, Reason: r.Why,
		})
	}
	return out
}

// FromRepairs reads them back, for the screen that reports them.
func FromRepairs(in []*pb.ThemeRepair) []design.Repair {
	if len(in) == 0 {
		return nil
	}
	out := make([]design.Repair, 0, len(in))
	for _, r := range in {
		out = append(out, design.Repair{
			Token: r.GetToken(), From: r.GetFrom(), To: r.GetTo(), Why: r.GetReason(),
		})
	}
	return out
}
