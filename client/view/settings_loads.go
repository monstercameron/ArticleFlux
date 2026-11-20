//go:build js && wasm

package view

// What a Settings tab has to have fetched before it can say anything true.
//
// One decision for every way a tab can come into force — a click, Back or
// Forward, or the address the page booted on. It used to live in the click
// handler alone, so /settings/podcast reached by URL or a reload painted the
// tab with nothing behind it: "checking" for ever on the server-key row, a
// model field reading "Default:" with no name, zeros that became real the first
// time the reader clicked away and back.

// settingsNeed is one fetch a tab depends on.
type settingsNeed int

const (
	needStats settingsNeed = iota
	needMyFeed
	needSmart
)

// settingsHave reports which of those fetches have already landed.
type settingsHave struct {
	stats, myFeed, smart bool
	// owner is whether the account may read the Smart+ config at all. A member's
	// request is refused, so asking for the Discover tab's sake would re-ask on
	// every visit and answer nothing.
	owner bool
}

// settingsNeeds is what must be fetched for tab, given what is already held.
//
// Only the tabs that show something the server knows carry a need, and a need
// already met is omitted: fetching on every visit would re-query while someone
// flicks through the tabs.
func settingsNeeds(tab settingsTab, have settingsHave) []settingsNeed {
	switch tab {
	case setServer, setActivity, setSpeed:
		if !have.stats {
			return []settingsNeed{needStats}
		}
	case setMyFeed:
		if !have.myFeed {
			return []settingsNeed{needMyFeed}
		}
	case setDiscover:
		// Whether the server has a key decides whether Smart+ review is offered at
		// all, and only an owner can be told.
		if !have.smart && have.owner {
			return []settingsNeed{needSmart}
		}
	case setSmart, setPodcast:
		// FluxCast reads the config too: its server-key row is a fact about the
		// deployment rather than a switch — see settingsPrereqs.
		if !have.smart {
			return []settingsNeed{needSmart}
		}
	}
	return nil
}
