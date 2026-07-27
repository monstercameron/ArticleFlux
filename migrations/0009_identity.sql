-- 0009_identity — roles, invites, devices, tokens, audit (plan.md §6.7, §6.8, §7).
--
-- Everything here is the identity surface D12 shaped. Invite-only, family and
-- friends: there is no registration table because there is no registration, and
-- an invite is the only way a second account comes into existence.
--
-- Two conventions differ from the DDL sketched in §6.8, deliberately and
-- throughout this file and the four that follow it:
--
--   Ids are TEXT, not INTEGER. `internal/idgen` produces sortable 128-bit ids,
--   and every shipped table from 0001 onward uses them. A foreign key whose type
--   does not match its referent is not a foreign key — SQLite will accept the
--   DDL and then never match a row — so this is not a style preference.
--
--   Timestamps are TEXT RFC3339, matching 0001–0006. The exception is
--   `engagements` (0007), which uses INTEGER milliseconds because every
--   derivation over it is arithmetic on time; that deviation is argued in §6.8
--   and is not extended here. Rule of thumb: rows that are *read* carry TEXT,
--   rows that are *computed over* carry INTEGER ms.

-- ---------------------------------------------------------------------------
-- Roles and capabilities
-- ---------------------------------------------------------------------------

-- Capabilities are a JSON set rather than columns, because 6.2's authz map is
-- the authority on what a capability means and a column per capability would put
-- that vocabulary in two places that can disagree.
--
-- tenant_id NULL means system-seeded: the four built-in roles exist before any
-- tenant does, and a tenant that has not customised anything holds no rows here.
CREATE TABLE roles (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT REFERENCES tenants(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    caps_json  TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX roles_tenant_name ON roles(ifnull(tenant_id, ''), lower(name));

CREATE TABLE user_roles (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    granted_at TEXT NOT NULL,
    PRIMARY KEY (user_id, role_id)
);

-- ---------------------------------------------------------------------------
-- Invites — the only path to a second account (D12)
-- ---------------------------------------------------------------------------

-- The code itself is never stored, only its hash: an invite code is a bearer
-- credential for the length of its life, and a database dump should not be a
-- pile of usable ones.
--
-- redeemed_by is kept after redemption rather than the row being deleted,
-- because "who let this person in" is the question an audit trail exists to
-- answer and it is unanswerable once the row is gone.
CREATE TABLE invites (
    code_hash   TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role_id     TEXT REFERENCES roles(id) ON DELETE SET NULL,
    label       TEXT NOT NULL DEFAULT '',
    created_by  TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    redeemed_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    redeemed_at TEXT,
    revoked_at  TEXT
);

CREATE INDEX invites_tenant ON invites(tenant_id, redeemed_at);

-- ---------------------------------------------------------------------------
-- Devices and refresh families (§7.1)
-- ---------------------------------------------------------------------------

-- A refresh token belongs to a family. Presenting a token that has already been
-- rotated means either a clone or a theft, and the response to both is the same:
-- revoke the whole family, not the one token. Storing family_id here is what
-- makes that a single UPDATE rather than a graph walk.
CREATE TABLE devices (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    label          TEXT,
    family_id      TEXT NOT NULL,
    refresh_hash   TEXT NOT NULL,
    -- §22.10's skew check reads this: a client cached by a Service Worker can be
    -- arbitrarily old, and the server has to be able to refuse it.
    client_version TEXT,
    user_agent     TEXT,
    created_at     TEXT NOT NULL,
    last_seen_at   TEXT,
    expires_at     TEXT,
    revoked_at     TEXT,
    revoked_reason TEXT
);

CREATE INDEX devices_family ON devices(family_id);
CREATE INDEX devices_user ON devices(user_id, revoked_at);

-- ---------------------------------------------------------------------------
-- API tokens (§15.2)
-- ---------------------------------------------------------------------------

-- scope is a fixed enum and is NEVER inherited from the owner's role. An admin
-- minting a token for a phone app must not be handing that app admin rights, and
-- the only reliable way to guarantee that is for the token's authority to be
-- written down independently of the user's.
CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    label        TEXT NOT NULL,
    scope        TEXT NOT NULL CHECK (scope IN ('reader_ro', 'reader_rw')),
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    expires_at   TEXT,
    revoked_at   TEXT
);

CREATE INDEX api_tokens_user ON api_tokens(user_id, revoked_at);

-- ---------------------------------------------------------------------------
-- Recovery, reset, and the lockout ledger (§7.1, §7.2)
-- ---------------------------------------------------------------------------

CREATE TABLE recovery_codes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    used_at    TEXT
);

CREATE INDEX recovery_codes_user ON recovery_codes(user_id, used_at);

CREATE TABLE reset_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issued_by  TEXT REFERENCES users(id) ON DELETE SET NULL,
    -- 'admin' covers the 7.10 break-glass path, 'cli' the operator on the box.
    -- 'email' is listed for completeness and does not ship: D14 rules out SMTP.
    origin     TEXT NOT NULL CHECK (origin IN ('admin', 'email', 'cli')),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);

-- Login attempts are the input to both halves of 6.1's rate limiting: per-user
-- lockout and per-IP throttling. Both are needed — per-user alone lets one
-- attacker lock every account out of spite, and per-IP alone does nothing about
-- a slow distributed guess at one password.
--
-- No REFERENCES on username or tenant_id: the interesting rows are the ones for
-- accounts that do not exist, and a foreign key would make those unwritable.
CREATE TABLE login_attempts (
    id        TEXT PRIMARY KEY,
    at        TEXT NOT NULL,
    username  TEXT,
    tenant_id TEXT,
    ip        TEXT,
    outcome   TEXT NOT NULL CHECK (outcome IN ('ok', 'bad_password', 'unknown_user', 'locked'))
);

CREATE INDEX login_attempts_user ON login_attempts(username, at DESC);
CREATE INDEX login_attempts_ip ON login_attempts(ip, at DESC);

-- ---------------------------------------------------------------------------
-- Audit log (§7.9)
-- ---------------------------------------------------------------------------

-- No REFERENCES on actor_user_id, and that is the point: §7.9 tombstones a
-- deleted user rather than erasing them, so the audit trail keeps naming an
-- actor who no longer has a row. A foreign key here would either block the
-- deletion or cascade the evidence away, and the whole value of an audit log is
-- that it survives the thing it describes.
--
-- acting_as_user_id records impersonation separately from the actor, so "admin
-- did X" and "admin did X while acting as member" are distinguishable.
CREATE TABLE audit_log (
    id                TEXT PRIMARY KEY,
    at                TEXT NOT NULL,
    actor_user_id     TEXT,
    acting_as_user_id TEXT,
    tenant_id         TEXT,
    action            TEXT NOT NULL,
    object_kind       TEXT,
    object_id         TEXT,
    detail_json       TEXT
);

CREATE INDEX audit_log_at ON audit_log(tenant_id, at DESC);
CREATE INDEX audit_log_actor ON audit_log(actor_user_id, at DESC);

-- ---------------------------------------------------------------------------
-- Sharing (§7.8)
-- ---------------------------------------------------------------------------

-- A grant is to a tenant OR to a user, and the CHECK enforces that at least one
-- is present. Both being set is legal and means "this user in that tenant".
CREATE TABLE shares (
    id                TEXT PRIMARY KEY,
    object_kind       TEXT NOT NULL CHECK (object_kind IN ('folder', 'view', 'bookmark_folder')),
    object_id         TEXT NOT NULL,
    owner_tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    owner_user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    grantee_tenant_id TEXT REFERENCES tenants(id) ON DELETE CASCADE,
    grantee_user_id   TEXT REFERENCES users(id) ON DELETE CASCADE,
    perm              TEXT NOT NULL CHECK (perm IN ('read', 'contribute')),
    created_by        TEXT NOT NULL REFERENCES users(id),
    created_at        TEXT NOT NULL,
    expires_at        TEXT,
    revoked_at        TEXT,
    CHECK (grantee_tenant_id IS NOT NULL OR grantee_user_id IS NOT NULL)
);

CREATE INDEX shares_grantee ON shares(grantee_tenant_id, grantee_user_id) WHERE revoked_at IS NULL;
CREATE INDEX shares_object ON shares(object_kind, object_id) WHERE revoked_at IS NULL;

-- Public shares are the unauthenticated surface (§7.8b, M21). The slug is a
-- 128-bit unguessable value from idgen, not a sequence: a public URL that can be
-- enumerated is a public URL that will be.
--
-- D16 fixed the policy: excerpt only, permanently. That is why there is no
-- body column here — the row points at an item and carries the sharer's own
-- comment, and republishing someone else's full text under your own name is a
-- licensing question rather than a feature.
CREATE TABLE public_shares (
    slug         TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id      TEXT NOT NULL REFERENCES items(id),
    comment      TEXT NOT NULL DEFAULT '',
    view_count   INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL,
    expires_at   TEXT,
    revoked_at   TEXT
);

CREATE INDEX public_shares_user ON public_shares(user_id, created_at DESC);
