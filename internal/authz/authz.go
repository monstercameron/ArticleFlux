// Package authz decides what a caller may do (TODO 6.2, plan.md §7.5).
//
// # One model, one map, one enforcement point
//
// §7.5: an interceptor resolves the credential into a Scope and rejects **before
// the handler runs**, against a static per-method capability map declared beside
// service registration. The sync API resolves its token through the same path
// and the same map — one authorization model, not two.
//
// Two models is the failure worth naming. A tunnel that checks roles in its
// interceptor and a REST API that checks them in each handler will disagree
// eventually, and the disagreement is discovered by someone doing through the
// API what the UI would not let them do.
//
// # A method with no entry fails CLOSED
//
// This is the property the whole package exists for. The alternative — unmapped
// means allowed — makes forgetting to add an entry a silent grant of the new
// RPC to everyone, and forgetting is exactly what happens when a method is added
// under time pressure. Failing closed makes the same mistake a loud 403 during
// development, which is the moment it costs nothing to fix.
//
// # Capabilities, not roles, at the call site
//
// The map names capabilities and roles hold sets of them. A handler asking "is
// this an admin" hard-codes a policy decision at four hundred call sites; a
// handler asking "may this caller mint an invite" asks a question the policy can
// answer differently later without touching the handler.
package authz

import (
	"fmt"
	"sort"
	"strings"
)

// Cap is one permission.
type Cap string

const (
	// Reading — what every account can do.
	CapReadItems  Cap = "items.read"
	CapWriteState Cap = "items.state"
	CapWriteNotes Cap = "notes.write"
	CapManageTags Cap = "tags.manage"

	// Organising the reader's own account.
	CapSubscribe   Cap = "feeds.subscribe"
	CapManageFeeds Cap = "feeds.manage"
	CapManageRules Cap = "rules.manage"
	CapManageViews Cap = "views.manage"
	CapImportData  Cap = "data.import"
	CapExportData  Cap = "data.export"

	// Tenant administration.
	CapInviteUsers    Cap = "tenant.invite"
	CapManageUsers    Cap = "tenant.users"
	CapTenantSettings Cap = "tenant.settings"
	CapReadAudit      Cap = "tenant.audit"

	// Instance operation.
	CapSystemSettings  Cap = "system.settings"
	CapManageTenants   Cap = "system.tenants"
	CapReadDiagnostics Cap = "system.diagnostics"
)

// Role names the four §6.7 roles.
type Role string

const (
	RoleViewer     Role = "viewer"
	RoleMember     Role = "member"
	RoleAdmin      Role = "admin"
	RoleSuperadmin Role = "superadmin"
)

// roleCaps is the built-in capability set per role.
//
// Viewer is genuinely read-only, including its own state: it cannot mark
// something read. That sounds harsh and is the point — a viewer is someone shown
// a shared folder, and read state written by a guest would appear in the owner's
// counts.
var roleCaps = map[Role][]Cap{
	RoleViewer: {
		CapReadItems,
	},
	RoleMember: {
		CapReadItems, CapWriteState, CapWriteNotes, CapManageTags,
		CapSubscribe, CapManageFeeds, CapManageRules, CapManageViews,
		CapImportData, CapExportData,
	},
	RoleAdmin: {
		CapReadItems, CapWriteState, CapWriteNotes, CapManageTags,
		CapSubscribe, CapManageFeeds, CapManageRules, CapManageViews,
		CapImportData, CapExportData,
		CapInviteUsers, CapManageUsers, CapTenantSettings, CapReadAudit,
	},
	RoleSuperadmin: {
		CapReadItems, CapWriteState, CapWriteNotes, CapManageTags,
		CapSubscribe, CapManageFeeds, CapManageRules, CapManageViews,
		CapImportData, CapExportData,
		CapInviteUsers, CapManageUsers, CapTenantSettings, CapReadAudit,
		CapSystemSettings, CapManageTenants, CapReadDiagnostics,
	},
}

// CapsForRole returns a role's capabilities, for seeding the roles table.
func CapsForRole(r Role) []string {
	caps := roleCaps[Role(strings.ToLower(string(r)))]
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, string(c))
	}
	sort.Strings(out)
	return out
}

// SystemRoles is every built-in role and its capabilities, for SeedSystemRoles.
func SystemRoles() map[string][]string {
	out := map[string][]string{}
	for role := range roleCaps {
		out[string(role)] = CapsForRole(role)
	}
	return out
}

// TokenScope limits what an API token may do, independently of its owner's role.
//
// §15.2, and the reason it is separate from the role: an admin minting a token
// for a phone app must not be handing that app admin rights. The effective
// capability set is the INTERSECTION of the role's and the scope's, so a token
// can only ever narrow.
type TokenScope string

const (
	// ScopeSession is a browser session: the full role.
	ScopeSession TokenScope = ""
	// ScopeReaderRO is a read-only sync token.
	ScopeReaderRO TokenScope = "reader_ro"
	// ScopeReaderRW is a read-write sync token.
	ScopeReaderRW TokenScope = "reader_rw"
)

var scopeCaps = map[TokenScope][]Cap{
	ScopeReaderRO: {CapReadItems},
	ScopeReaderRW: {CapReadItems, CapWriteState, CapWriteNotes, CapManageTags, CapSubscribe},
}

// Caller is a resolved credential.
type Caller struct {
	TenantID string
	UserID   string
	Role     Role
	// TokenScope narrows the role when the credential is an API token.
	TokenScope TokenScope
	// Extra are capabilities from roles granted through user_roles, beyond the
	// one named on the users row.
	Extra []Cap
}

// Caps computes the effective capability set.
func (c Caller) Caps() map[Cap]bool {
	out := map[Cap]bool{}
	for _, cap := range roleCaps[Role(strings.ToLower(string(c.Role)))] {
		out[cap] = true
	}
	for _, cap := range c.Extra {
		out[cap] = true
	}

	if c.TokenScope == ScopeSession {
		return out
	}
	// A token narrows. The intersection, never the union — a token scope that
	// could ADD a capability would make minting one a privilege escalation.
	allowed := scopeCaps[c.TokenScope]
	narrowed := map[Cap]bool{}
	for _, cap := range allowed {
		if out[cap] {
			narrowed[cap] = true
		}
	}
	return narrowed
}

// Can reports whether the caller holds a capability.
func (c Caller) Can(cap Cap) bool { return c.Caps()[cap] }

// Map is the static per-method capability map (§7.5).
type Map struct {
	methods map[string]Cap
	// public names methods that need no capability at all — login, health.
	// Listed explicitly rather than inferred, so "this RPC is unauthenticated"
	// is a decision someone made rather than an entry someone forgot.
	public map[string]bool
}

// NewMap builds an empty map.
func NewMap() *Map {
	return &Map{methods: map[string]Cap{}, public: map[string]bool{}}
}

// Require declares that a method needs a capability.
func (m *Map) Require(method string, cap Cap) *Map {
	m.methods[method] = cap
	return m
}

// Public declares that a method needs no credential.
func (m *Map) Public(methods ...string) *Map {
	for _, method := range methods {
		m.public[method] = true
	}
	return m
}

// ErrNotMapped means a method has no entry. Distinguished from a plain denial so
// the log can say "someone added an RPC and forgot the map" rather than
// "permission denied", which reads as a policy decision.
type ErrNotMapped struct{ Method string }

func (e ErrNotMapped) Error() string {
	return fmt.Sprintf("authz: %s has no capability mapping, so it is denied", e.Method)
}

// ErrDenied means the caller lacks the capability.
type ErrDenied struct {
	Method string
	Cap    Cap
}

func (e ErrDenied) Error() string {
	return fmt.Sprintf("authz: %s requires %s", e.Method, e.Cap)
}

// Check authorises one call.
//
// Returns nil to permit. An unmapped method is DENIED — see the package comment;
// this is the property everything else rests on.
func (m *Map) Check(method string, c Caller) error {
	if m.public[method] {
		return nil
	}
	cap, ok := m.methods[method]
	if !ok {
		return ErrNotMapped{Method: method}
	}
	if !c.Can(cap) {
		return ErrDenied{Method: method, Cap: cap}
	}
	return nil
}

// Unmapped returns the methods in `known` that have no entry.
//
// For a startup check and a test: §7.5's fail-closed default is correct at
// runtime and useless as the first notice, because the first notice is a user
// getting a 403 on a feature that shipped. Comparing the map against the
// service's actual method list at boot turns that into a refusal to start.
func (m *Map) Unmapped(known []string) []string {
	var out []string
	for _, method := range known {
		if m.public[method] {
			continue
		}
		if _, ok := m.methods[method]; !ok {
			out = append(out, method)
		}
	}
	sort.Strings(out)
	return out
}

// Methods lists every mapped method, for diagnostics.
func (m *Map) Methods() map[string]Cap {
	out := make(map[string]Cap, len(m.methods))
	for k, v := range m.methods {
		out[k] = v
	}
	return out
}
