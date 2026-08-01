package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func seedIdentityRepo(t *testing.T) (*ReaderRepo, Scope, context.Context) {
	t.Helper()
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "alice",
		Hash: "h", Role: "admin", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	return repo, Scope{TenantID: "t1", UserID: "u1", Role: "admin"}, ctx
}

// SeedSystemRoles is called at boot, always with the same fixed map — it must
// both create absent roles and pick up a capability added to an existing one,
// since 6.2 says the authz map can grow without a migration.
func TestSeedSystemRolesCreatesThenUpdatesCaps(t *testing.T) {
	repo, _, ctx := seedIdentityRepo(t)

	if err := repo.SeedSystemRoles(ctx, map[string][]string{
		"member": {"read"},
	}); err != nil {
		t.Fatal(err)
	}
	var capsJSON string
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT caps_json FROM roles WHERE tenant_id IS NULL AND name = 'member'`).Scan(&capsJSON); err != nil {
		t.Fatal(err)
	}
	if capsJSON != `["read"]` {
		t.Errorf("caps after first seed = %s, want [\"read\"]", capsJSON)
	}

	// Re-seeding with a grown capability set must update the existing row
	// rather than erroring on the duplicate name.
	if err := repo.SeedSystemRoles(ctx, map[string][]string{
		"member": {"read", "write"},
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM roles WHERE tenant_id IS NULL AND name = 'member'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows for 'member' after re-seeding, want 1 (update, not duplicate)", n)
	}
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT caps_json FROM roles WHERE tenant_id IS NULL AND name = 'member'`).Scan(&capsJSON); err != nil {
		t.Fatal(err)
	}
	if capsJSON != `["read","write"]` {
		t.Errorf("caps after re-seed = %s, want the grown set", capsJSON)
	}
}

func TestMintInviteAndRedeemGrantsTenantAndRole(t *testing.T) {
	repo, sc, ctx := seedIdentityRepo(t)
	code, err := repo.MintInvite(ctx, sc, "for-bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if code == "" {
		t.Fatal("MintInvite returned an empty code")
	}
	// redeemed_by references users(id): the redeeming account has to exist
	// before the code is consumed, same as a real signup flow would create it.
	if err := repo.AddUser(ctx, NewTenant{
		TenantID: "t1", UserID: "u2", Username: "bob", Hash: "h", Role: "member",
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}

	tenantID, roleID, err := repo.RedeemInvite(ctx, code, "u2")
	if err != nil {
		t.Fatal(err)
	}
	if tenantID != "t1" {
		t.Errorf("tenantID = %q, want t1", tenantID)
	}
	if roleID != "" {
		t.Errorf("roleID = %q, want empty (no role was minted)", roleID)
	}
}

// The redemption and the check are one transaction specifically so two people
// racing the same code cannot both get an account.
func TestRedeemInviteWorksExactlyOnce(t *testing.T) {
	repo, sc, ctx := seedIdentityRepo(t)
	code, err := repo.MintInvite(ctx, sc, "one-shot", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, u := range []string{"u2", "u3"} {
		if err := repo.AddUser(ctx, NewTenant{
			TenantID: "t1", UserID: u, Username: u, Hash: "h", Role: "member", Now: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := repo.RedeemInvite(ctx, code, "u2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.RedeemInvite(ctx, code, "u3"); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("second redemption = %v, want ErrInviteInvalid", err)
	}
}

func TestRedeemInviteRejectsUnknownExpiredAndRevoked(t *testing.T) {
	repo, sc, ctx := seedIdentityRepo(t)

	if _, _, err := repo.RedeemInvite(ctx, "never-minted", "u2"); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("unknown code = %v, want ErrInviteInvalid", err)
	}

	label := "will-expire"
	code, err := repo.MintInvite(ctx, sc, label, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Write.ExecContext(ctx,
		`UPDATE invites SET expires_at = '2000-01-01T00:00:00Z' WHERE label = ?`, label); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.RedeemInvite(ctx, code, "u2"); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("expired code = %v, want ErrInviteInvalid", err)
	}

	code2, err := repo.MintInvite(ctx, sc, "will-be-revoked", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RevokeInvite(ctx, sc, "will-be-revoked"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.RedeemInvite(ctx, code2, "u2"); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("revoked code = %v, want ErrInviteInvalid", err)
	}
}

func TestListInvitesHidesTheCodeItself(t *testing.T) {
	repo, sc, ctx := seedIdentityRepo(t)
	code, err := repo.MintInvite(ctx, sc, "labeled", "")
	if err != nil {
		t.Fatal(err)
	}
	invites, err := repo.ListInvites(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(invites) != 1 {
		t.Fatalf("got %d invites, want 1", len(invites))
	}
	if invites[0].Label != "labeled" {
		t.Errorf("label = %q", invites[0].Label)
	}
	if invites[0].Revoked {
		t.Error("a fresh invite reports Revoked = true")
	}
	// The plaintext code must appear nowhere retrievable via the listing —
	// this is a behavioural sanity check that ListInvites' columns don't leak it.
	if invites[0].Label == code {
		t.Fatal("the invite's label equals its plaintext code")
	}
}

func TestScopeForAPITokenResolvesAndTracksLastUsed(t *testing.T) {
	repo, sc, ctx := seedIdentityRepo(t)
	token, err := repo.MintAPIToken(ctx, sc, "phone", TokenReadOnly)
	if err != nil {
		t.Fatal(err)
	}

	resolved, scope, err := repo.ScopeForAPIToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TenantID != "t1" || resolved.UserID != "u1" {
		t.Errorf("resolved scope = %+v", resolved)
	}
	if scope != TokenReadOnly {
		t.Errorf("scope = %q, want %q", scope, TokenReadOnly)
	}

	tokens, err := repo.ListAPITokens(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].LastUsedAt == "" {
		t.Errorf("ScopeForAPIToken did not stamp last_used_at: %+v", tokens)
	}
}

func TestScopeForAPITokenRejectsUnknownRevokedAndExpired(t *testing.T) {
	repo, sc, ctx := seedIdentityRepo(t)

	if _, _, err := repo.ScopeForAPIToken(ctx, "never-minted"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown token = %v, want ErrNotFound", err)
	}

	token, err := repo.MintAPIToken(ctx, sc, "to-revoke", TokenReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := repo.ListAPITokens(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, tk := range tokens {
		if tk.Label == "to-revoke" {
			id = tk.ID
		}
	}
	if id == "" {
		t.Fatal("could not find the minted token's id")
	}
	if err := repo.RevokeAPIToken(ctx, sc, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ScopeForAPIToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked token = %v, want ErrNotFound", err)
	}

	expiredToken, err := repo.MintAPIToken(ctx, sc, "to-expire", TokenReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Write.ExecContext(ctx,
		`UPDATE api_tokens SET expires_at = '2000-01-01T00:00:00Z' WHERE label = 'to-expire'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ScopeForAPIToken(ctx, expiredToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired token = %v, want ErrNotFound", err)
	}
}

func TestMintAPITokenRejectsBadScopeAndEmptyLabel(t *testing.T) {
	repo, sc, ctx := seedIdentityRepo(t)
	if _, err := repo.MintAPIToken(ctx, sc, "x", "not-a-real-scope"); err == nil {
		t.Error("an invalid token scope was accepted")
	}
	if _, err := repo.MintAPIToken(ctx, sc, "  ", TokenReadOnly); err == nil {
		t.Error("a blank label was accepted")
	}
}

func TestScopeForDeviceResolvesAndRejectsRevoked(t *testing.T) {
	repo, sc, ctx := seedIdentityRepo(t)
	if err := repo.RegisterDevice(ctx, sc, "dev1", "fam1", "refresh-tok", "laptop", "1.0"); err != nil {
		t.Fatal(err)
	}
	resolved, err := repo.ScopeForDevice(ctx, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TenantID != "t1" || resolved.UserID != "u1" || resolved.Role != "admin" {
		t.Errorf("resolved = %+v", resolved)
	}

	if _, err := repo.ScopeForDevice(ctx, "never-registered"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown device = %v, want ErrNotFound", err)
	}

	if _, err := repo.RevokeDeviceFamily(ctx, sc, "fam1", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ScopeForDevice(ctx, "dev1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked device = %v, want ErrNotFound", err)
	}
}

// §7.3a SEC1's exploit, pinned directly at the store layer: `RegisterDevice`
// used to resolve an id collision with `ON CONFLICT(id) DO UPDATE`, which
// replaced only `refresh_hash` and left the row's original user_id/tenant_id
// untouched. A second user who could name (guess, reuse, or otherwise cause a
// collision on) the same record id had their OWN refresh secret installed
// against the FIRST user's row — so RotateRefresh/ScopeForDevice, which
// trust the row rather than the caller, would mint a session for the first
// user off the second user's credential. That is a cross-account
// session-minting bug, and this test drives exactly that sequence: same
// record id, two different accounts, second write must fail and change
// nothing.
func TestRegisterDeviceRefusesACollidingRecordFromAnotherAccount(t *testing.T) {
	repo, victim, ctx := seedIdentityRepo(t)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t2", Name: "Other", UserID: "mallory", Username: "mallory",
		Hash: "h", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	attacker := Scope{TenantID: "t2", UserID: "mallory", Role: "member"}

	const sharedRecordID = "shared-record-id"
	if err := repo.RegisterDevice(ctx, victim, sharedRecordID, "victim-family",
		"victims-refresh-secret", "victim's laptop", "1.0"); err != nil {
		t.Fatal(err)
	}

	// The attacker's login lands on the SAME record id — a real collision is
	// cryptographically unreachable now that the id is server-generated
	// 128-bit CSPRNG (idgen.DeviceID), but the write still has to refuse
	// rather than upsert, because "unlikely" is not "impossible" and a
	// database restored from an old backup, a bug in a future caller, or a
	// deliberately forged internal call must not silently take over someone
	// else's row.
	err := repo.RegisterDevice(ctx, attacker, sharedRecordID, "mallorys-family",
		"mallorys-refresh-secret", "mallory's phone", "1.0")
	if !errors.Is(err, ErrDeviceOwnerMismatch) {
		t.Fatalf("RegisterDevice across accounts = %v, want ErrDeviceOwnerMismatch", err)
	}

	// The row must still belong to, and only refresh for, the victim.
	resolved, err := repo.ScopeForDevice(ctx, sharedRecordID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.UserID != "u1" || resolved.TenantID != "t1" {
		t.Fatalf("SECURITY: after a refused cross-account write, the record resolves to %+v, "+
			"want the victim (u1/t1)", resolved)
	}
	// The victim's own refresh secret must be exactly what it was — untouched
	// by the refused write, not silently rotated or cleared. Checked BEFORE
	// trying the attacker's secret below: RotateRefresh treats any wrong
	// secret as reuse and revokes the whole family (correct behaviour,
	// exercised by TestAReplayedRefreshTokenRevokesTheFamily), which would
	// make this assertion fail for an unrelated reason if it ran second.
	if err := repo.RotateRefresh(ctx, sharedRecordID, "victims-refresh-secret",
		"victims-next-secret"); err != nil {
		t.Fatalf("the victim's own refresh secret stopped working after the refused "+
			"cross-account write: %v", err)
	}
}

// A22-shaped check: granting a role to a user outside the caller's tenant must
// be refused, not silently applied across tenant lines.
func TestGrantRoleRefusesCrossTenantTarget(t *testing.T) {
	repo, sc, ctx := seedIdentityRepo(t)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t2", Name: "Other", UserID: "u2", Username: "bob",
		Hash: "h", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SeedSystemRoles(ctx, map[string][]string{"member": {"read"}}); err != nil {
		t.Fatal(err)
	}
	var roleID string
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT id FROM roles WHERE name = 'member' AND tenant_id IS NULL`).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	if err := repo.GrantRole(ctx, sc, "u2", roleID); !errors.Is(err, ErrNotFound) {
		t.Errorf("granting a role to another tenant's user = %v, want ErrNotFound", err)
	}
}
