//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// What the diagnostics tabs say when the call behind them failed.
//
// These pin a bug that was invisible precisely because it only appeared when
// something else was already wrong. `loadStats` leaves `loading` false and
// `stats` nil on a failure, and two of the three tabs it feeds read only those
// two fields — so Speed rendered its loading skeleton forever, and Activity fell
// through to its empty state and reported "nothing has happened yet" on behalf
// of a server it had not managed to ask. The Server tab, fed by the same call,
// had checked the error all along.
//
// The failure worth defending against is a screen that is confidently wrong
// while the sentence explaining it sits unread in state.

func tabHTML(t *testing.T, p settingsProps) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		var body []ui.Node
		switch p.tab {
		case setSpeed:
			body = settingsSpeed(tr, p)
		case setActivity:
			body = settingsActivity(tr, p)
		default:
			t.Fatalf("tabHTML does not render %q", p.tab)
		}
		return html.Div(html.Props{}, body...)
	})
}

// TestSpeedReportsAFailedFetchInsteadOfSpinning is the permanent-skeleton case.
func TestSpeedReportsAFailedFetchInsteadOfSpinning(t *testing.T) {
	out := tabHTML(t, settingsProps{tab: setSpeed, statsErr: "the server did not answer"})

	if !strings.Contains(out, "the server did not answer") {
		t.Fatalf("the Speed tab does not report the failure it was handed:\n%s", out)
	}
	// The skeleton is the specific wrong answer here: it says "still loading"
	// about a call that finished, and it never stops saying it.
	if strings.Contains(out, "sk-line") {
		t.Errorf("the Speed tab is still showing its loading skeleton after a failure:\n%s", out)
	}
}

// TestActivityDoesNotCallAFailedFetchAnEmptyLog is the mirror case, and the
// worse of the two: a skeleton is at least ambiguous, while "nothing has
// happened" is a false claim about the server.
func TestActivityDoesNotCallAFailedFetchAnEmptyLog(t *testing.T) {
	out := tabHTML(t, settingsProps{tab: setActivity, logsErr: "the log could not be read"})

	if !strings.Contains(out, "the log could not be read") {
		t.Fatalf("the Activity tab does not report the failure it was handed:\n%s", out)
	}
	// The catalogue's own words (settings.activityEmpty), not a paraphrase: an
	// assertion against copy that does not exist passes whatever the code does,
	// which is the failure mode a negative check is most prone to.
	if strings.Contains(out, "Nothing at this level yet") {
		t.Errorf("the Activity tab reports an empty log for a fetch that failed:\n%s", out)
	}
	// The retry has to survive the error, which is why this one appends to the
	// head rather than replacing the panel the way Speed does: the level chips
	// and Reload are the only way back from here without reloading the page.
	if !strings.Contains(out, `data-action="settings-refresh"`) {
		t.Errorf("a failed Activity tab has no way to try again:\n%s", out)
	}
}

// TestActivityKeepsTheRecordsItAlreadyHad pins the half-failure: the log call
// failed on a REFRESH, so there are still records on screen from the last one.
// Throwing them away would punish the reader for pressing Reload.
func TestActivityKeepsTheRecordsItAlreadyHad(t *testing.T) {
	out := tabHTML(t, settingsProps{
		tab:     setActivity,
		logsErr: "the log could not be read",
		logs: []*pb.LogRecord{
			{Time: "2026-08-08T10:00:00Z", Level: "ERROR", Message: "poll failed"},
		},
		logViewport: 400,
	})

	if !strings.Contains(out, "the log could not be read") {
		t.Errorf("stale records are shown with no sign the reload failed:\n%s", out)
	}
	if !strings.Contains(out, "poll failed") {
		t.Errorf("a failed reload threw away the records it already had:\n%s", out)
	}
}
