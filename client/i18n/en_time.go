package i18n

// Time words.
//
// relTime renders the dateline on every list row and every article eyebrow,
// which makes it the most-rendered string in the application — and it was the
// last one still emitting English from Go's own time formatting. `t.Format("2
// Jan")` produces "Jan", "Feb", "Mar" no matter what language the interface is
// in, because Go's layouts are not locale-aware and never will be.
//
// So the month names are a catalog namespace keyed by month number, and the
// compact form is a pattern. Both are needed: a language that writes the month
// first ("Jan 2") reorders the pattern, and one that abbreviates differently
// replaces the names.
func init() {
	text(DefaultLocale, "month", map[string]string{
		"1": "Jan", "2": "Feb", "3": "Mar", "4": "Apr",
		"5": "May", "6": "Jun", "7": "Jul", "8": "Aug",
		"9": "Sep", "10": "Oct", "11": "Nov", "12": "Dec",
	})

	text(DefaultLocale, "time", map[string]string{
		// Anything under a minute old. A word rather than "0m", because "0
		// minutes ago" is not how anyone says it in any language.
		"now": "now",
		// The compact dateline for anything older than a week. Day and month
		// only — the list row has no space for a year, and an article old
		// enough for the year to matter is old enough that the exact date is
		// not what the reader is scanning for.
		"dayMonth": "{day} {month}",
	})
}
