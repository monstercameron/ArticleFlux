package authn

import (
	"strings"
	"testing"
)

// The shape rules that let one screen accept both kinds of recovery credential.
//
// These decide ROUTING, never authorisation — the server verifies whatever it is
// handed — but a routing mistake here is indistinguishable from a bad credential
// to the reader, because both answer the same uniform refusal. So the rules are
// pinned against real values from the generators rather than against literals
// somebody typed, which is the only way this stays true if either format moves.

func TestAGeneratedRecoveryCodeLooksLikeOne(t *testing.T) {
	sheet, err := GenerateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range sheet {
		if !LooksLikeRecoveryCode(code) {
			t.Errorf("%q came out of GenerateRecoveryCodes and is not recognised as a code", code)
		}
	}
}

// However it was written down and typed back. Lower case, no dashes, spaces
// instead of dashes — all of it is presentation the normaliser removes, and the
// shape test has to see through it or the reader gets routed to the wrong RPC.
func TestARecoveryCodeIsRecognisedHoweverItWasTypedBack(t *testing.T) {
	sheet, err := GenerateRecoveryCodes(1)
	if err != nil {
		t.Fatal(err)
	}
	code := sheet[0]

	for name, typed := range map[string]string{
		"as issued":   code,
		"lower case":  strings.ToLower(code),
		"no dashes":   strings.ReplaceAll(code, "-", ""),
		"with spaces": strings.ReplaceAll(code, "-", " "),
		"padded":      "  " + code + "  ",
	} {
		if !LooksLikeRecoveryCode(typed) {
			t.Errorf("a code typed %s was not recognised: %q", name, typed)
		}
	}
}

func TestAResetTokenDoesNotLookLikeARecoveryCode(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if LooksLikeRecoveryCode(token) {
		t.Errorf("a reset token was routed as a recovery code: %q", token)
	}
}

// What a reader actually pastes is the whole link, because that is what copying
// one gives them. A field that only took the bare token would make every reader
// hand-edit a URL.
func TestExtractResetTokenSurvivesWhateverWasPasted(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}

	for name, pasted := range map[string]string{
		"the bare token":      token,
		"a full link":         "https://reader.example.com/reset?token=" + token,
		"a link with a hash":  "https://reader.example.com/reset?token=" + token + "#top",
		"a link with params":  "https://reader.example.com/reset?token=" + token + "&from=email",
		"a link with padding": "  https://reader.example.com/reset?token=" + token + "  \n",
	} {
		if got := ExtractResetToken(pasted); got != token {
			t.Errorf("%s: extracted %q, want %q", name, got, token)
		}
	}
}

// It must not invent a token out of something that has none. Returning the whole
// pasted string is correct — the server will refuse it — but it must not return
// a fragment that happens to look plausible.
func TestExtractResetTokenLeavesUnrelatedTextAlone(t *testing.T) {
	if got := ExtractResetToken("  nonsense  "); got != "nonsense" {
		t.Errorf("extracted %q from text with no token", got)
	}
}
