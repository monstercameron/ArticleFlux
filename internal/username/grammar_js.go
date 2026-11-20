//go:build js

package username

// grammar, in the browser, has no opinion.
//
// The setup form calls Check so that the common mistakes — `owner`, a name in
// front of the address, `cam@laptop` — are answered as the reader leaves the
// field rather than after a round trip, and every one of those is caught by the
// checks around this call, which are the same code on both sides. What is left
// for a parser is the exotic end of RFC 5322 (comments, quoted local parts,
// encoded words), and deciding that in the browser meant linking net/mail and
// what it pulls in: 78 KB of a bundle with a ceiling, measured, to pre-empt an
// answer the server gives anyway.
//
// So the browser is deliberately never STRICTER than the server: anything this
// lets through that the server would refuse comes back as the server's own
// refusal, on the same field. The other direction — refusing here an address
// the server would have taken — is the one that would be a bug, and returning
// nil cannot do it.
func grammar(string) error { return nil }
