-- 0032_security_retention — the indexes the security tables' housekeeping needs.
--
-- `login_attempts` and `audit_log` are the two tables in this schema that are
-- written on every login of every account forever and read almost never. 0009
-- gave both of them the indexes their READS want — (username, at), (ip, at),
-- (tenant_id, at), (actor_user_id, at) — and neither of the two indexes their
-- growth needs.
--
-- # Why `at` alone, when (tenant_id, at) already exists
--
-- A composite index is only usable for an ordering when the query constrains
-- every column to its left. `AuditTrailInstance` deliberately does not — it is
-- the instance-wide view and filters by no tenant at all, which is the whole
-- reason it exists as a separate reader from `AuditTrail` — so
-- `ORDER BY at DESC LIMIT 100` against `audit_log_at` cannot be answered by a
-- seek. SQLite scans the table and sorts it, and the cost of that grows with
-- the log rather than with the hundred rows asked for. The same shape applies
-- to `DELETE FROM login_attempts WHERE at < ?`: no username, no IP, so neither
-- of 0009's indexes helps and the purge reads every row it is not deleting.
--
-- These are the leading-column-free versions of both, which is the one thing
-- neither table had.
--
-- # Why the retention this pairs with lives in Go rather than in a trigger
--
-- A trigger would delete on write, at unpredictable moments, inside somebody
-- else's transaction — a login that takes an extra second because it happened
-- to be the one that crossed the window. The sweep is on the same timer as
-- `retention_sweeps` (0023) and answerable to the same setting, which also
-- means an operator can read what it did instead of inferring it from rows that
-- are no longer there.

-- The instance-wide audit view's ordering (store.AuditTrailInstance) and the
-- audit purge's range scan, both.
CREATE INDEX audit_log_at_only ON audit_log(at DESC);

-- store.PurgeLoginAttempts's range scan. Ascending rather than DESC: this one
-- exists for `WHERE at < ?`, which walks forward from the oldest row and stops,
-- and never for an ordering.
CREATE INDEX login_attempts_at_only ON login_attempts(at);
