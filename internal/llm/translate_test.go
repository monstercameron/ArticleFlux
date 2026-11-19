package llm

import "testing"

// §10.5b's boundary, audited on the assembled body like every other one.

func TestTranslatePayloadCarriesOnlyWhatIsPermitted(t *testing.T) {
	p := TranslatePayload{
		Target: "fr (French)",
		Title:  "Emergency patch closes a zero day",
		Segments: []TranslateSegment{
			{ID: 0, Text: "Administrators should apply the update "},
			{ID: 1, Text: "immediately"},
		},
	}
	bad, err := AuditTranslate(mustJSON(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("the translate payload carried keys §10.5b does not permit: %v", bad)
	}
	// And it must fail the interest layer's audit — the lists are distinct.
	if bad, _ := AuditEgress(mustJSON(t, p)); len(bad) == 0 {
		t.Fatal("a translate payload passed the §18.8 interest audit: the lists are not distinct")
	}
}

func TestAuditTranslateRejectsSmuggledKeys(t *testing.T) {
	for _, k := range []string{"url", "author", "source", "item_id", "html", "href", "user_id"} {
		body := []byte(`{"target":"fr","segments":[{"id":0,"text":"x"}],"` + k + `":"y"}`)
		bad, err := AuditTranslate(body)
		if err != nil {
			t.Fatal(err)
		}
		if len(bad) != 1 || bad[0] != k {
			t.Errorf("AuditTranslate let %q through: %v", k, bad)
		}
	}
}

func TestTranslateKeysAreNotInTheInterestList(t *testing.T) {
	for _, k := range []string{"segments", "text", "target"} {
		if EgressKeys[k] {
			t.Errorf("EgressKeys admits %q, which only §10.5b's payload carries", k)
		}
	}
}
