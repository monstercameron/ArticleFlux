package smart

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// OutlineJSON turns an API response into the smallest thing a model can pick
// field paths out of.
//
// The same argument distill() makes for HTML, with one difference that matters:
// a JSON response is mostly CONTENT — every chapter title, every description,
// every URL — where an HTML page is mostly markup. So this keeps the SHAPE and
// throws the volume away: every key, the type of its value, one truncated
// sample, and for arrays the length plus a single representative entry.
//
// A 121 KB response of 170 chapters becomes about 1 KB describing one chapter
// and saying there are 170 of them. That is everything the question needs —
// which key holds the title, which holds the link, which holds the date — and it
// is 1% of the egress.
//
// The output is deliberately not JSON. It cannot be parsed back into data, which
// keeps it a description rather than a copy.
const (
	// jsonMaxBytes caps the outline. Reached only by responses with hundreds of
	// distinct keys, which are configuration dumps rather than lists.
	jsonMaxBytes = 12 << 10
	// jsonMaxDepth stops the walk. An entry's fields are one level down from the
	// array; six is generous for the wrappers people put around it.
	jsonMaxDepth = 6
	// jsonSample is how much of a value is shown. Enough to recognise an ISO
	// date, a slug, a relative URL — which is what the choice turns on.
	jsonSample = 48
	// jsonMaxKeys bounds one object. Beyond this the object is a lookup table,
	// not a record, and listing all of it teaches nothing.
	jsonMaxKeys = 30
)

func OutlineJSON(body []byte) string {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return ""
	}
	var b strings.Builder
	writeJSON(&b, root, 0, "")
	return b.String()
}

func writeJSON(b *strings.Builder, node any, depth int, key string) {
	if b.Len() > jsonMaxBytes || depth > jsonMaxDepth {
		return
	}
	pad := strings.Repeat("  ", depth)
	label := key
	if label != "" {
		label += ": "
	}

	switch v := node.(type) {
	case map[string]any:
		b.WriteString(pad + label + "{" + strconv.Itoa(len(v)) + " fields}\n")
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		// Sorted, so the same response produces the same outline every time and
		// two runs are comparable when something looks wrong.
		sort.Strings(keys)
		if len(keys) > jsonMaxKeys {
			keys = keys[:jsonMaxKeys]
		}
		for _, k := range keys {
			writeJSON(b, v[k], depth+1, k)
		}

	case []any:
		// The length is the single most useful fact about an array here: it is
		// how the model tells the list of entries from the list of genres.
		b.WriteString(pad + label + "[" + strconv.Itoa(len(v)) + "]\n")
		if len(v) == 0 {
			return
		}
		// One representative entry. Arrays in these responses are homogeneous,
		// and showing a second is a second copy of the same shape.
		writeJSON(b, v[0], depth+1, "[0]")

	case string:
		b.WriteString(pad + label + "string " + quoteSample(v) + "\n")
	case float64:
		if v == float64(int64(v)) {
			b.WriteString(pad + label + "number " + strconv.FormatInt(int64(v), 10) + "\n")
			return
		}
		b.WriteString(pad + label + "number " + strconv.FormatFloat(v, 'f', -1, 64) + "\n")
	case bool:
		b.WriteString(pad + label + "bool " + strconv.FormatBool(v) + "\n")
	case nil:
		// Null is worth showing rather than skipping: a field that is null on
		// this entry is a field the model must not choose as the title.
		b.WriteString(pad + label + "null\n")
	}
}

func quoteSample(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > jsonSample {
		s = s[:jsonSample] + "…"
	}
	return `"` + strings.ReplaceAll(s, `"`, "'") + `"`
}
