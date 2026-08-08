package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/audit"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// `articleflux audit` — the read half of §7.9.
//
// # Why this is a CLI and not an RPC
//
// The audit log spent its whole existence with no writer and no reader, which
// is what §7.3c is fixing. Adding an RPC that no screen calls would recreate
// exactly that: a correct component with no consumer, indistinguishable from a
// feature until somebody needs it. `CapReadAudit` exists and an admin screen
// should eventually use it — but the screen is the part that makes an RPC real,
// and there is no screen yet.
//
// Meanwhile the audience for an audit trail on a self-hosted box is the person
// who runs the box, and they have a shell. That is not a consolation prize: an
// operator investigating "did somebody get into my account" wants grep, a pipe
// and a date range, not a paginated table in a browser they may be locked out
// of.
//
// When the admin screen lands, it calls `AuditTrail` through an RPC and this
// keeps working unchanged — they are two readers of one query, which is the
// arrangement the store layer was already written for.

// auditCmd prints the trail, newest first.
func auditCmd(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	dbPath := commonFlags(fs)
	limit := fs.Int("n", 50, "how many entries to show (max 500)")
	since := fs.Duration("since", 0, "only entries newer than this (e.g. 24h, 168h); 0 for no limit")
	only := fs.String("action", "", "comma-separated actions to include (e.g. auth.login,auth.lockout)")
	alerts := fs.Bool("alerts", false, "only the entries classified as alerts — everything except routine sign-in and sign-out")
	asJSON := fs.Bool("json", false, "emit JSON lines instead of a table, for piping")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	a, err := openAdmin(ctx, log, *dbPath)
	if err != nil {
		return err
	}
	defer a.Close()

	// The INSTANCE view, not the tenant-scoped one, and the difference is not
	// cosmetic: a login lockout is recorded before the account is resolved — the
	// username may not name one — so those rows carry no tenant and
	// `AuditTrail` filters them out. An operator asking "was I attacked" and
	// being shown a trail with every lockout silently missing would be worse
	// than showing them nothing.
	//
	// Safe here for a reason that does not generalise to an RPC: the caller has
	// a shell and the database file. See store.AuditTrailInstance.
	//
	// Over-fetch when filtering, because the filters below run in Go and the
	// store's LIMIT is applied before them. Asking for 50 and printing 3 because
	// 47 were sign-ins is a worse answer than the one the operator wanted.
	fetch := *limit
	if *since > 0 || *only != "" || *alerts {
		fetch = 500
	}
	entries, err := a.Repo().AuditTrailInstance(ctx, fetch)
	if err != nil {
		return err
	}

	wanted := map[string]bool{}
	for _, name := range strings.Split(*only, ",") {
		if n := strings.TrimSpace(name); n != "" {
			wanted[n] = true
		}
	}

	cut := time.Time{}
	if *since > 0 {
		cut = time.Now().UTC().Add(-*since)
	}

	var shown []store.AuditEntry
	for _, e := range entries {
		if len(wanted) > 0 && !wanted[e.Action] {
			continue
		}
		if *alerts && audit.Action(e.Action).Severity() != audit.Alert {
			continue
		}
		if !cut.IsZero() {
			at, perr := time.Parse(time.RFC3339Nano, e.At)
			// An unparseable stamp is SHOWN, not dropped. A corrupt row in an
			// audit log is itself worth seeing, and silently filtering it would
			// hide the one entry somebody tampered with.
			if perr == nil && at.Before(cut) {
				continue
			}
		}
		shown = append(shown, e)
		if len(shown) >= *limit {
			break
		}
	}

	if len(shown) == 0 {
		// Said out loud rather than printing nothing. An empty table and a broken
		// query look identical, and this command exists precisely because "the
		// audit log appeared to be empty" was true for the wrong reason once.
		fmt.Fprintln(os.Stderr, "No matching audit entries. (An instance that has never been "+
			"signed in to will have none; anything older than your -since window is excluded.)")
		return nil
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		for _, e := range shown {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
		return nil
	}

	// Resolve user ids to names once. The trail stores ids because §7.9 requires
	// it to survive the account being deleted, but an operator reading it wants
	// names — and looking up each row separately would be a query per line.
	names := resolveActorNames(ctx, a.Repo(), shown)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "WHEN\tACTION\tACTOR\tSUBJECT\tDETAIL")
	for _, e := range shown {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			shortTime(e.At), e.Action,
			nameOr(names, e.ActorUserID, "—"),
			nameOr(names, e.ActingAsUser, "—"),
			flattenDetail(e.Detail))
	}
	return w.Flush()
}

// resolveActorNames maps the user ids in a page of entries to usernames.
//
// Best-effort: an id that no longer resolves is a TOMBSTONED user, which §7.9
// explicitly expects the trail to keep naming. Those print as the raw id rather
// than being blanked — "an account that no longer exists did this" is
// information, and hiding it would defeat the reason the column has no foreign
// key.
func resolveActorNames(ctx context.Context, repo *store.ReaderRepo,
	entries []store.AuditEntry) map[string]string {

	ids := map[string]bool{}
	for _, e := range entries {
		if e.ActorUserID != "" {
			ids[e.ActorUserID] = true
		}
		if e.ActingAsUser != "" {
			ids[e.ActingAsUser] = true
		}
	}
	out := make(map[string]string, len(ids))
	for id := range ids {
		u, err := repo.UserForRecovery(ctx, id)
		if err == nil {
			out[id] = u.Username
		}
	}
	return out
}

func nameOr(names map[string]string, id, empty string) string {
	if id == "" {
		return empty
	}
	if n, ok := names[id]; ok {
		return n
	}
	// A deleted account, shown as its id and marked so nobody reads the hex as a
	// username.
	return id + " (deleted)"
}

// shortTime trims the stamp to something a terminal column can hold. The full
// value is always available with -json.
func shortTime(at string) string {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return at
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// flattenDetail renders the JSON blob as sorted key=value pairs.
//
// Sorted, because map iteration order is random and an audit trail whose columns
// move between rows is one nobody can scan or diff. Sorting also means two runs
// of this command over the same rows produce identical output, which is what
// makes it usable in a script.
func flattenDetail(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, " ")
}
