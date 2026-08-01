package smart

import (
	"strings"
	"testing"
)

// distilljson.go's happy path (a real chapters array) is exercised end to
// end via ProposeJSON in scrapejson_llm_test.go. This file is the value-type
// branches that fixture never needs: bools, nulls, non-integer numbers, and
// the three caps (depth, key count, sample length).

func TestOutlineJSONRendersEveryScalarType(t *testing.T) {
	out := OutlineJSON([]byte(`{"title":"A","views":41.5,"archived":true,"deleted_at":null}`))
	for _, want := range []string{"number 41.5", "bool true", "null", `string "A"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestOutlineJSONWholeNumberFloatsRenderWithoutADecimalPoint(t *testing.T) {
	out := OutlineJSON([]byte(`{"count":3}`))
	if !strings.Contains(out, "number 3\n") {
		t.Errorf("a whole-number float rendered with a decimal point:\n%s", out)
	}
}

// jsonMaxKeys bounds one object: beyond it, the object is a lookup table and
// the outline stops listing keys rather than growing without bound.
func TestOutlineJSONCapsKeysPerObject(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < jsonMaxKeys+10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"k` + string(rune('a'+i%26)) + string(rune('0'+i/26)) + `":1`)
	}
	b.WriteString("}")
	out := OutlineJSON([]byte(b.String()))
	if got := strings.Count(out, "number 1"); got > jsonMaxKeys {
		t.Errorf("listed %d keys, want at most jsonMaxKeys (%d)", got, jsonMaxKeys)
	}
}

// jsonMaxDepth stops the walk; a response nested well past it must not
// recurse forever or blow up the outline.
func TestOutlineJSONStopsAtMaxDepth(t *testing.T) {
	body := strings.Repeat(`{"n":`, jsonMaxDepth+10) + "1" + strings.Repeat("}", jsonMaxDepth+10)
	out := OutlineJSON([]byte(body))
	if strings.Count(out, "n:") > jsonMaxDepth+2 {
		t.Errorf("nesting past jsonMaxDepth was not capped:\n%s", out)
	}
}

// An empty array still reports its length (zero) rather than nothing at
// all — "there is an array here and it is empty" is itself useful.
func TestOutlineJSONEmptyArrayReportsItsLength(t *testing.T) {
	out := OutlineJSON([]byte(`{"items":[]}`))
	if !strings.Contains(out, "[0]") {
		t.Errorf("an empty array did not report a length of 0:\n%s", out)
	}
}

// quoteSample truncates long string values rather than reproducing an
// article body in the shape description.
func TestQuoteSampleTruncatesLongStrings(t *testing.T) {
	long := strings.Repeat("word ", jsonSample)
	got := quoteSample(long)
	if strings.Contains(got, long) {
		t.Error("a long value reached the sample unclipped")
	}
	if !strings.Contains(got, "…") {
		t.Errorf("a truncated sample did not carry the ellipsis marker: %q", got)
	}
}
