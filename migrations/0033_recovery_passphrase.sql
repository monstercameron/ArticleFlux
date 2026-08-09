-- A recovery passphrase: a second way back in that the reader chooses and can
-- remember, beside the sheet of codes the server generates.
--
-- # Why it is a separate table and not a column on users
--
-- It is a CREDENTIAL, and credentials on this schema live where they can be
-- revoked, counted and reasoned about on their own — recovery_codes and
-- reset_tokens both do. A nullable column on `users` would make "does this
-- account have one" a property of the account row, which is where password_hash
-- already lives, and the two would be read together often enough that somebody
-- would eventually compare the wrong one.
--
-- One per user, enforced by the primary key rather than by the application:
-- replacing a passphrase must not be able to leave two.
--
-- # Why the hash is not the recovery_codes hash
--
-- A recovery code is 80 bits of server-generated entropy, so `recovery_codes`
-- stores a fast hash of it — brute force is not the threat when guessing means
-- searching a space that size. A passphrase is CHOSEN BY A PERSON, which is the
-- opposite: it is short, it is memorable, and an attacker with the database can
-- run a wordlist against it. So this column holds an Argon2id hash, the same
-- primitive `users.password_hash` uses and for the same reason.
--
-- The distinction is the whole security argument for this feature being safe,
-- and it is written here because the two tables otherwise look alike enough to
-- invite somebody to "simplify" them together.
CREATE TABLE recovery_passphrases (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- Argon2id, via internal/secret. Never a digest of the phrase itself.
    hash       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    -- The last time it was SPENT. Unlike a recovery code this is not consumed —
    -- a passphrase somebody memorised and can only use once is a passphrase they
    -- will find has stopped working at the moment they need it — so this records
    -- use rather than preventing it, and gives the Activity tab something to
    -- show when one is used.
    used_at    TEXT
);
