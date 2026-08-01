package authz

import (
	"errors"
	"testing"
)

// §7.5's central property, and the one everything else rests on: a method with
// no entry is DENIED. The alternative makes forgetting an entry a silent grant
// of a new RPC to everyone, and forgetting is exactly what happens when a method
// is added under time pressure.
func TestAnUnmappedMethodFailsClosed(t *testing.T) {
	m := NewMap().Require("/v1/ListItems", CapReadItems)
	admin := Caller{Role: RoleSuperadmin}

	if err := m.Check("/v1/ListItems", admin); err != nil {
		t.Fatalf("a mapped method was denied: %v", err)
	}

	err := m.Check("/v1/SomethingAddedYesterday", admin)
	if err == nil {
		t.Fatal("an unmapped method was permitted — even a superadmin must not " +
			"reach an RPC nobody has classified")
	}
	// Distinguished from a plain denial so the log says "someone forgot the map"
	// rather than "permission denied", which reads as a policy decision.
	var notMapped ErrNotMapped
	if !errors.As(err, &notMapped) {
		t.Errorf("= %T, want ErrNotMapped", err)
	}
}

func TestRolesGrantWhatTheyShould(t *testing.T) {
	cases := []struct {
		role   Role
		can    []Cap
		cannot []Cap
	}{
		{RoleViewer,
			[]Cap{CapReadItems},
			// A viewer is someone shown a shared folder, and read state written
			// by a guest would appear in the owner's counts.
			[]Cap{CapWriteState, CapWriteNotes, CapSubscribe, CapInviteUsers}},
		{RoleMember,
			[]Cap{CapReadItems, CapWriteState, CapWriteNotes, CapSubscribe, CapManageRules},
			[]Cap{CapInviteUsers, CapManageUsers, CapSystemSettings}},
		{RoleAdmin,
			[]Cap{CapReadItems, CapWriteState, CapInviteUsers, CapManageUsers, CapReadAudit},
			[]Cap{CapSystemSettings, CapManageTenants}},
		{RoleSuperadmin,
			[]Cap{CapSystemSettings, CapManageTenants, CapReadDiagnostics, CapInviteUsers},
			nil},
	}
	for _, c := range cases {
		t.Run(string(c.role), func(t *testing.T) {
			caller := Caller{Role: c.role}
			for _, cap := range c.can {
				if !caller.Can(cap) {
					t.Errorf("%s cannot %s", c.role, cap)
				}
			}
			for _, cap := range c.cannot {
				if caller.Can(cap) {
					t.Errorf("%s can %s and should not", c.role, cap)
				}
			}
		})
	}
}

// §15.2: an admin minting a token for a phone app must not be handing that app
// admin rights. A token can only ever NARROW.
func TestATokenScopeNarrowsAndNeverWidens(t *testing.T) {
	admin := Caller{Role: RoleAdmin}
	if !admin.Can(CapInviteUsers) {
		t.Fatal("the fixture is wrong")
	}

	roToken := Caller{Role: RoleAdmin, TokenScope: ScopeReaderRO}
	if roToken.Can(CapInviteUsers) {
		t.Error("a read-only token inherited the owner's admin rights")
	}
	if roToken.Can(CapWriteState) {
		t.Error("a read-only token can write state")
	}
	if !roToken.Can(CapReadItems) {
		t.Error("a read-only token cannot read")
	}

	rwToken := Caller{Role: RoleAdmin, TokenScope: ScopeReaderRW}
	if !rwToken.Can(CapWriteState) {
		t.Error("a read-write token cannot mark items read")
	}
	if rwToken.Can(CapManageUsers) {
		t.Error("a read-write token inherited user management")
	}

	// And the intersection, not the union: a VIEWER's read-write token must not
	// gain write, or minting a token would be a privilege escalation.
	viewerToken := Caller{Role: RoleViewer, TokenScope: ScopeReaderRW}
	if viewerToken.Can(CapWriteState) {
		t.Error("a token widened a viewer's rights; minting one is now an escalation")
	}
}

// Extra roles granted through user_roles must take effect, or an instance where
// an admin granted a second role behaves as though they had not.
func TestGrantedCapabilitiesAdd(t *testing.T) {
	member := Caller{Role: RoleMember}
	if member.Can(CapReadAudit) {
		t.Fatal("the fixture is wrong")
	}
	withExtra := Caller{Role: RoleMember, Extra: []Cap{CapReadAudit}}
	if !withExtra.Can(CapReadAudit) {
		t.Error("a granted capability had no effect")
	}
	// But a token still narrows it.
	tokened := Caller{Role: RoleMember, Extra: []Cap{CapReadAudit}, TokenScope: ScopeReaderRO}
	if tokened.Can(CapReadAudit) {
		t.Error("a granted capability escaped the token scope")
	}
}

// "This RPC is unauthenticated" must be a decision someone made rather than an
// entry someone forgot, so it is declared rather than inferred.
func TestPublicMethodsAreDeclared(t *testing.T) {
	m := NewMap().
		Require("/v1/ListItems", CapReadItems).
		Public("/v1/Login", "/v1/Health")

	empty := Caller{}
	if err := m.Check("/v1/Login", empty); err != nil {
		t.Errorf("a public method required a credential: %v", err)
	}
	if err := m.Check("/v1/ListItems", empty); err == nil {
		t.Error("an anonymous caller reached a capability-gated method")
	}
}

// §7.5's fail-closed default is right at runtime and useless as the first
// notice, because the first notice is a user getting a 403 on a shipped feature.
// Comparing the map against the service's real method list at boot turns that
// into a refusal to start.
func TestUnmappedFindsTheGapsAtBoot(t *testing.T) {
	m := NewMap().
		Require("/v1/ListItems", CapReadItems).
		Public("/v1/Login")

	known := []string{"/v1/ListItems", "/v1/Login", "/v1/DeleteEverything", "/v1/NewThing"}
	missing := m.Unmapped(known)

	if len(missing) != 2 {
		t.Fatalf("Unmapped = %v, want the two unclassified methods", missing)
	}
	if missing[0] != "/v1/DeleteEverything" || missing[1] != "/v1/NewThing" {
		t.Errorf("Unmapped = %v", missing)
	}
	if len(m.Unmapped([]string{"/v1/ListItems", "/v1/Login"})) != 0 {
		t.Error("a fully mapped service reported gaps")
	}
}

// The roles table is seeded from this, so the two cannot disagree about what a
// role means.
func TestSystemRolesCoverEveryRole(t *testing.T) {
	roles := SystemRoles()
	for _, want := range []Role{RoleViewer, RoleMember, RoleAdmin, RoleSuperadmin} {
		caps, ok := roles[string(want)]
		if !ok {
			t.Errorf("%s is not seeded", want)
			continue
		}
		if len(caps) == 0 {
			t.Errorf("%s is seeded with no capabilities", want)
		}
	}
	// Sorted, so re-seeding does not rewrite the row with the same set in a
	// different order and make every boot look like a change.
	caps := CapsForRole(RoleMember)
	for i := 1; i < len(caps); i++ {
		if caps[i] < caps[i-1] {
			t.Errorf("capabilities are not sorted: %v", caps)
			break
		}
	}
}

// A denial names the capability, so a log line says what was needed rather than
// only that something was refused.
func TestDenialsNameTheCapability(t *testing.T) {
	m := NewMap().Require("/v1/MintInvite", CapInviteUsers)
	err := m.Check("/v1/MintInvite", Caller{Role: RoleMember})
	if err == nil {
		t.Fatal("a member minted an invite")
	}
	var denied ErrDenied
	if !errors.As(err, &denied) {
		t.Fatalf("= %T, want ErrDenied", err)
	}
	if denied.Cap != CapInviteUsers || denied.Method != "/v1/MintInvite" {
		t.Errorf("denial = %+v", denied)
	}
}

// An unknown role grants nothing. A typo in a database row must not become a
// permission set.
func TestAnUnknownRoleGrantsNothing(t *testing.T) {
	c := Caller{Role: Role("adminstrator")}
	if len(c.Caps()) != 0 {
		t.Errorf("a misspelt role granted %d capabilities", len(c.Caps()))
	}
	m := NewMap().Require("/v1/ListItems", CapReadItems)
	if err := m.Check("/v1/ListItems", c); err == nil {
		t.Error("a misspelt role was permitted")
	}
}

// Case is not a permission boundary: a database row saying "Admin" must behave
// as admin, or an instance seeded by hand silently loses its administrator.
func TestRoleMatchingIsCaseInsensitive(t *testing.T) {
	if !(Caller{Role: Role("ADMIN")}).Can(CapInviteUsers) {
		t.Error("an uppercase role lost its capabilities")
	}
}

// IsPublic is what the interceptor asks BEFORE resolving a credential, so it
// must answer for both declared-public and ordinary methods without needing one.
func TestIsPublicReflectsThePublicList(t *testing.T) {
	m := NewMap().Require("/v1/ListItems", CapReadItems).Public("/v1/Login")
	if !m.IsPublic("/v1/Login") {
		t.Error("a declared-public method reported as not public")
	}
	if m.IsPublic("/v1/ListItems") {
		t.Error("a capability-gated method reported as public")
	}
	if m.IsPublic("/v1/NeverDeclared") {
		t.Error("an unmapped method reported as public")
	}
}

// The error strings are what ends up in a log line, so the two must stay
// distinguishable from each other by more than type.
func TestErrorMessagesNameWhatWentWrong(t *testing.T) {
	notMapped := ErrNotMapped{Method: "/v1/NewThing"}
	if got := notMapped.Error(); got != "authz: /v1/NewThing has no capability mapping, so it is denied" {
		t.Errorf("ErrNotMapped.Error() = %q", got)
	}

	denied := ErrDenied{Method: "/v1/MintInvite", Cap: CapInviteUsers}
	if got := denied.Error(); got != "authz: /v1/MintInvite requires tenant.invite" {
		t.Errorf("ErrDenied.Error() = %q", got)
	}
}

// Methods is a diagnostic snapshot, so it must reflect what was Required and
// must not let the caller mutate the map's own state through the copy.
func TestMethodsListsWhatWasMapped(t *testing.T) {
	m := NewMap().
		Require("/v1/ListItems", CapReadItems).
		Require("/v1/MintInvite", CapInviteUsers)

	got := m.Methods()
	if len(got) != 2 {
		t.Fatalf("Methods() = %v, want 2 entries", got)
	}
	if got["/v1/ListItems"] != CapReadItems || got["/v1/MintInvite"] != CapInviteUsers {
		t.Errorf("Methods() = %v", got)
	}

	got["/v1/ListItems"] = CapSystemSettings
	if m.Check("/v1/ListItems", Caller{Role: RoleSuperadmin}) != nil {
		t.Error("mutating the returned map changed the map's own requirement")
	}
}
