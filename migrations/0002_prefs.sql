-- 0002_prefs — per-user UI preferences.
--
-- Server-side, not localStorage. Pane widths, density and theme are part of "my
-- account", not "this browser": a reader that forgets your layout when you open
-- it on the laptop instead of the desktop has not remembered anything useful.
-- It also means preferences survive a cleared cache, which localStorage does not.
--
-- Key/value rather than a column per preference. Preferences are the fastest-
-- growing surface in an application like this (§8 lists dozens), and a migration
-- per checkbox is how a schema turns into a settings dialog.
CREATE TABLE user_prefs (
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, key)
);

CREATE INDEX user_prefs_tenant ON user_prefs(tenant_id, user_id);
