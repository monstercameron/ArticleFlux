//go:build js && wasm

package view

import "testing"

// "Does this server have a key" — and the third answer.
//
// Reported: pressing Podcast produced a silent show complaining that the voice
// was not working, on an instance where everything was enabled and the server
// was successfully writing segments and synthesising audio. Pressing next, or
// play/pause, then started it.
//
// The cause was two answers where there are three. Inside the show the key is
// inferred from whether the story on screen came with a listening ticket, and
// at the instant a show starts that story's body has only just been ASKED for.
// Absent evidence read as absent key: slideListenOn set the voice line and
// returned without ever starting the narrator, and because that line is
// non-empty, slideStep's own narrate was gated off too. Only play/pause — which
// calls listen() directly and consults no prerequisites — could start it.

func TestTheConfigOutranksTheInference(t *testing.T) {
	// Asked directly and answered. Nothing about a body can overrule it: the
	// article on screen may be one that was fetched before a key was pasted in.
	if present, known := slideKeyKnown(true, true, false, false); !present || !known {
		t.Errorf("config says yes: present=%v known=%v", present, known)
	}
	if present, known := slideKeyKnown(true, false, true, true); present || !known {
		t.Errorf("config says no: present=%v known=%v", present, known)
	}
}

func TestAFetchedArticleIsEvidenceBothWays(t *testing.T) {
	// SpeechURL mints a ticket only on an instance that can synthesise, so a
	// body that landed WITHOUT one genuinely proves there is no key — that is
	// not an unknown, and treating it as one would never report a real problem.
	if present, known := slideKeyKnown(false, false, true, true); !present || !known {
		t.Errorf("a ticket on a fetched article: present=%v known=%v", present, known)
	}
	if present, known := slideKeyKnown(false, false, true, false); present || !known {
		t.Errorf("no ticket on a fetched article: present=%v known=%v", present, known)
	}
}

func TestTheMomentAShowStartsIsUnknownNotAbsent(t *testing.T) {
	// The bug, exactly. slideOpen has asked for the body and nothing has come
	// back. There is no evidence in either direction and the honest report is
	// "not yet".
	present, known := slideKeyKnown(false, false, false, false)
	if known {
		t.Error("no config and no body was reported as a known answer")
	}
	if present {
		t.Error("no evidence was reported as a present key")
	}
}

func TestAnUnknownKeyDoesNotRefuseToStart(t *testing.T) {
	// The fix. Being wrong this way costs one request that answers 501 and
	// reports itself at once. Being wrong the other way costs a show that never
	// starts and blames the server.
	list := slidePrereqs(true, true, true, false) // key reads absent, but see below
	if got := slideStartBlockedBy(list, false); got != "" {
		t.Errorf("an unknown key blocked the start on %q", got)
	}
	// And when it IS known to be absent, it blocks — the message is true then,
	// and there is a real remedy behind it.
	if got := slideStartBlockedBy(list, true); got != prereqServerKey {
		t.Errorf("a known-absent key did not block: %q", got)
	}
}

func TestTheReadersOwnSwitchesAlwaysBlock(t *testing.T) {
	// Those are known the instant they are asked, and the remedy is a switch
	// the reader can reach. Never starting on one of those would be starting a
	// mode they explicitly turned off.
	off := slidePrereqs(false, true, true, true)
	if got := slideStartBlockedBy(off, false); got == "" {
		t.Error("Smart+ voice off did not block the start")
	}
	noPodcast := slidePrereqs(true, false, true, true)
	if got := slideStartBlockedBy(noPodcast, false); got == "" && slidePrereqsMet(noPodcast) == false {
		t.Error("a missing reader-owned requirement did not block the start")
	}
}

func TestEverythingOnStartsWhateverIsKnown(t *testing.T) {
	all := slidePrereqs(true, true, true, true)
	for _, known := range []bool{true, false} {
		if got := slideStartBlockedBy(all, known); got != "" {
			t.Errorf("known=%v: blocked on %q with everything on", known, got)
		}
	}
}
