//go:build js && wasm

package view

import "testing"

// Every tab that shows something the server knows names the fetch it waits for,
// and stops naming it once that fetch has landed — the rule that has to hold for
// a tab reached by address exactly as it does for a click.
func TestSettingsNeeds(t *testing.T) {
	cases := []struct {
		tab  settingsTab
		have settingsHave
		want []settingsNeed
	}{
		{setServer, settingsHave{}, []settingsNeed{needStats}},
		{setActivity, settingsHave{}, []settingsNeed{needStats}},
		{setSpeed, settingsHave{}, []settingsNeed{needStats}},
		{setMyFeed, settingsHave{}, []settingsNeed{needMyFeed}},
		{setSmart, settingsHave{}, []settingsNeed{needSmart}},
		{setPodcast, settingsHave{}, []settingsNeed{needSmart}},
		{setServer, settingsHave{stats: true}, nil},
		{setMyFeed, settingsHave{myFeed: true}, nil},
		{setSmart, settingsHave{smart: true}, nil},
		{setPodcast, settingsHave{smart: true}, nil},
		// Another tab's data does not satisfy this one's.
		{setPodcast, settingsHave{stats: true, myFeed: true}, []settingsNeed{needSmart}},
		{setReading, settingsHave{}, nil},
		{setAppearance, settingsHave{}, nil},
		{setAccount, settingsHave{}, nil},
	}
	for _, c := range cases {
		got := settingsNeeds(c.tab, c.have)
		if len(got) != len(c.want) || (len(got) == 1 && got[0] != c.want[0]) {
			t.Errorf("settingsNeeds(%q, %+v) = %v, want %v", c.tab, c.have, got, c.want)
		}
	}
}
