package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The limits count characters, and every refusal is the typed error the
// transport turns into a keyed message (G7, G19 of the second UX audit).

func asText(t *testing.T, err error, subject TextSubject, problem TextProblem, max int) {
	t.Helper()
	var te *TextError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want a *TextError", err)
	}
	if te.Subject != subject || te.Problem != problem || te.Max != max {
		t.Errorf("got %+v, want subject=%s problem=%s max=%d", te, subject, problem, max)
	}
}

func TestFolderNamesAreCountedInCharactersAndRefusedWithTheLimit(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	// 48 two-byte letters is 96 bytes: a fine name. 49 is one too many.
	if _, err := repo.CreateFolder(ctx, sc, strings.Repeat("é", MaxFolderName)); err != nil {
		t.Errorf("%d characters refused: %v", MaxFolderName, err)
	}
	_, err := repo.CreateFolder(ctx, sc, strings.Repeat("é", MaxFolderName+1))
	asText(t, err, SubjectFolder, ProblemTooLong, MaxFolderName)
	_, err = repo.CreateFolder(ctx, sc, "   ")
	asText(t, err, SubjectFolder, ProblemEmpty, 0)

	a, err := repo.CreateFolder(ctx, sc, "Tech")
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.CreateFolder(ctx, sc, "News")
	if err != nil {
		t.Fatal(err)
	}
	// Renaming onto another folder's name, in any case.
	for _, name := range []string{"Tech", "tech", "TECH"} {
		_, err = repo.RenameFolder(ctx, sc, b.ID, name)
		asText(t, err, SubjectFolder, ProblemTaken, 0)
	}
	_, err = repo.RenameFolder(ctx, sc, b.ID, strings.Repeat("x", MaxFolderName+1))
	asText(t, err, SubjectFolder, ProblemTooLong, MaxFolderName)
	// Recasing one's own name is not a clash.
	if _, err := repo.RenameFolder(ctx, sc, a.ID, "TECH"); err != nil {
		t.Errorf("recasing a folder's own name: %v", err)
	}
}

func TestNotesAreCountedInCharacters(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	err := repo.SetNote(context.Background(), sc, "no-such-item", strings.Repeat("é", MaxNoteChars+1))
	asText(t, err, SubjectNote, ProblemTooLong, MaxNoteChars)
	// Within the limit the size check passes and the item lookup is what answers.
	err = repo.SetNote(context.Background(), sc, "no-such-item", strings.Repeat("é", MaxNoteChars))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("a note of exactly %d characters: %v, want to get as far as the item lookup", MaxNoteChars, err)
	}
}
