package jsonsel

import "testing"

// FuzzExtract feeds arbitrary bytes through Extract as an API response —
// exactly what jsonsel exists for: the endpoint behind a JS-rendered page,
// answering with whatever it answers with. The rule is fixed and known-good;
// only the body varies, matching how one compiled rule runs against many
// polls of the same endpoint.
func FuzzExtract(f *testing.F) {
	c, err := Compile(good())
	if err != nil {
		f.Fatalf("Compile: %v", err)
	}
	seeds := []string{
		body,
		"", "not json", "{", "null", "[]", "{}",
		`{"comic":{"chapters":"not an array"}}`,
		`{"comic":{"chapters":[1,2,3]}}`,
		`{"comic":{"chapters":[{"full_title":null,"url":null}]}}`,
		`{"comic":{"chapters":[{"full_title":"A","url":"javascript:alert(1)"}]}}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Extract panicked on %q: %v", in, r)
			}
		}()
		res, err := Extract(c, []byte(in), now)
		if err != nil {
			return // "not JSON" is a documented, expected error
		}
		if len(res.Items) > MaxItems {
			t.Fatalf("Extract(%q) returned %d items, over the %d cap", in, len(res.Items), MaxItems)
		}
	})
}
