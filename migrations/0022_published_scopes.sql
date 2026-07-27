-- 0022_published_scopes — publishing a folder or a tag as an Atom feed (§7.8b, TODO F29).
--
-- # Three tables named for sharing, and they are three different things
--
-- `shares` (0009) is a private GRANT: this folder, to that user, read or
-- contribute. `public_shares` (0009) is one ITEM published at its own slug with
-- the sharer's comment — §7.8b's per-item half, still unbuilt. This is the
-- third: a whole SCOPE the reader already curates, published as a feed anybody
-- can subscribe to.
--
-- They are deliberately not merged. A grant has a grantee and a permission; an
-- item share has a comment and a view count; a published scope has a title and
-- an indexing policy. One table carrying all of that would be mostly NULL and
-- would need a discriminator column to tell you which third of it applied.
--
-- Google Reader's social layer was sharing, and its removal is the thing people
-- still bring up eighteen years later. This is the honest version of it for a
-- self-hosted reader: no accounts to follow, no server of ours in the middle —
-- your own instance publishes an Atom feed at an address only the people you
-- gave it to know, and any reader in the world can subscribe to it.
--
-- # The slug IS the credential
--
-- 128 bits from a CSPRNG, unique across the instance. There is no permission
-- check on the read side because there is no identity on the read side: whoever
-- holds the address may read it, which is what makes it work in an RSS reader
-- that will never log in. That is also why it is rotatable — rotation is the
-- only revocation available against somebody who already has the URL, and it
-- necessarily breaks existing subscribers, which the UI has to say out loud.
--
-- # Why a share points at a SCOPE rather than holding items
--
-- The scope the reader already curates — a folder, a tag — is the thing they
-- mean when they say "share my Rust reading". A second collection to maintain
-- would go stale the first week. `kind` + `target_id` name it; adding a kind
-- later is a migration nobody has to think about, and a share whose target is
-- deleted is a share that publishes nothing rather than one that dangles.
CREATE TABLE published_scopes (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- 'folder' | 'tag'. Checked here rather than in Go so a bad row cannot exist
    -- even if something writes around the repository.
    kind       TEXT NOT NULL CHECK (kind IN ('folder', 'tag')),
    target_id  TEXT NOT NULL,
    -- What the feed calls itself. Separate from the folder's own name because a
    -- share is a publication: "Rust" is a fine folder and a poor feed title, and
    -- renaming the folder should not rename something strangers are subscribed
    -- to.
    title      TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    -- Search engines are off by default. A share is unguessable, not secret, and
    -- the difference stops mattering the moment a crawler indexes it — so
    -- opting IN is the only safe direction for a default nobody will revisit.
    indexable  INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    -- Revoked rather than deleted: a revoked slug must keep answering 404 rather
    -- than becoming available for reuse, and an audit trail of what was once
    -- public is worth more than the row it costs.
    revoked_at TEXT
);

CREATE INDEX published_scopes_owner ON published_scopes(user_id, revoked_at);
