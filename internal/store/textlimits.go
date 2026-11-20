package store

import (
	"fmt"
	"unicode/utf8"
)

// The size limits on what a reader types — a folder's name, a tag, a note — and
// the one typed error all of them return.
//
// # Why this is a type and not a fmt.Errorf
//
// Each of these used to be a bare fmt.Errorf, which the transport could only turn
// into either "Something went wrong on the server" (no mapping) or the error's
// own English (InvalidArgument with err.Error()). Neither can be translated, and
// neither can say WHICH limit — and "which limit" is the whole message: a reader
// who typed 105 characters needs to be told 48, not that the tag could not be
// added (G7 and G19 of the second UX audit). So the error carries the subject,
// the problem and the number, and internal/apierr turns the triple into a
// catalog key and an argument.
//
// # Characters, not bytes
//
// The limits count runes. They counted bytes (`len`) until the audit, which made
// "Café ☕ crème brûlée" cost 28 against a limit the reader was counting in
// letters, and made a name in Japanese or Cyrillic fit a third as many
// characters as the same name in English. A reader can count what they typed;
// they cannot count its UTF-8. A rune is not a grapheme cluster (a flag emoji is
// two), but it is what Go counts cheaply, it errs in the reader's favour for
// every precomposed accent, and the browser's own maxlength counts UTF-16 units
// — so the client's cap is never looser than this one for text in the BMP.

// MaxTagName bounds a tag's name, the same 48 the rail has room to draw.
const MaxTagName = 48

// MaxNoteChars bounds one note. Eight thousand characters is several pages of
// prose and small enough that the column never becomes a file store. It was
// 8 KiB of BYTES (MaxNoteBytes); see the package comment above for why it is
// now characters, and why the round number.
const MaxNoteChars = 8000

// TextSubject is which thing a reader typed.
type TextSubject string

const (
	SubjectFolder   TextSubject = "folder"
	SubjectTag      TextSubject = "tag"
	SubjectTagLabel TextSubject = "tagLabel"
	SubjectNote     TextSubject = "note"
)

// TextProblem is what was wrong with it.
type TextProblem string

const (
	// ProblemEmpty — nothing, or only spaces, where a name is required.
	ProblemEmpty TextProblem = "empty"
	// ProblemTooLong — over Max characters.
	ProblemTooLong TextProblem = "tooLong"
	// ProblemTaken — another one of the reader's own already has the name.
	ProblemTaken TextProblem = "taken"
)

// TextError is a refused piece of reader-typed text.
type TextError struct {
	Subject TextSubject
	Problem TextProblem
	// Max is the limit, in characters; zero where the problem is not a limit.
	Max int
}

func (e *TextError) Error() string {
	switch e.Problem {
	case ProblemEmpty:
		return fmt.Sprintf("store: a %s needs a name", e.Subject)
	case ProblemTaken:
		return fmt.Sprintf("store: there is already a %s with that name", e.Subject)
	default:
		return fmt.Sprintf("store: a %s must be at most %d characters", e.Subject, e.Max)
	}
}

// chars is what a reader would count: runes.
func chars(s string) int { return utf8.RuneCountInString(s) }
