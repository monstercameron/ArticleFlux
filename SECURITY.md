# Security

Tidings is a self-hosted, multi-tenant reader. Two properties matter more than anything else in it,
because both fail *silently*: **one tenant must never see another's reading history**, and **nothing
a user reads leaves the machine unless they turned that on.** Everything below is downstream of those.

## Reporting a vulnerability

Open a private security advisory on the repository, or email the maintainer. Please do not open a
public issue for anything that affects a running instance.

Include what you did, what you expected, and what happened. A proof of concept against a local
instance is welcome; please do not test against somebody else's.

Expect an acknowledgement within a few days. This is a personal project, not a vendor with an
on-call rota — the response is best effort, and it is honest about that rather than promising a
window it cannot keep.

## The threat model

An instance is expected to be reachable from the internet, with accounts on it, fetching arbitrary
URLs that its users chose. That last clause is the interesting one: **a feed reader is a
server-side request forgery engine that its users are supposed to point wherever they like.**

## Deliberate positions

### Outbound fetches go through an SSRF guard, always

Every fetch — feed polling, favicon retrieval, feed discovery — passes `internal/netguard`.

`-allow-private` exists so a self-hosted instance can subscribe to something on its own LAN. It was
originally a switch that disabled the guard outright, and because the dev server sets it
automatically on a loopback bind, **the single most commonly-run configuration had no SSRF
protection at all.** It is now a *narrower deny list*: RFC1918 and loopback become reachable,
link-local and the cloud metadata endpoint never do, on any configuration.

If you are adding an outbound call, it goes through the guard. There is no fast path.

### The dev server's missing login is tied to the bind address

`./make.ps1 dev` serves without authentication so that the loop stays short. That is only ever true
on a loopback bind — binding a real interface turns it off, because an internet-facing instance with
it on would be an open reader. The behaviour is coupled to the bind on purpose, so that "it worked
in dev" cannot ship.

### Tenant isolation is structural, not conditional

Sources and items are global and deduplicated; a popular feed is polled once for the whole server.
Per-user state — read, starred, notes, tags, ratings — lives in its own tables keyed by account.

Two consequences that look like quirks and are not:

- **Global rows are never hard-deleted.** A cascade from `items` would destroy every other tenant's
  history along with the unsubscribing user's.
- **A shared setting says so before you change it.** Poll interval lives on the source, not the
  subscription; the settings panel groups by *who a setting belongs to* and tells you how many other
  people are affected.

Any change here needs its isolation test extended in the same commit. This is one of the two things
that is never silent-decidable (see `CONTRIBUTING.md`).

### The model egress boundary is opt-in and default-off

Article text is not sent anywhere by default. The synthesised-speech feature uses the browser's own
voice, locally and offline, unless a user explicitly enables the Smart+ voice — which is a per-user
switch that defaults to off and calls out through a host allowlist.

Adding a model call, changing what it is given, or adding a host to that allowlist is a change to a
security boundary. It gets asked about, written into the plan, and tested.

## Operating an instance

- **The database is the sensitive artefact.** `tidings.db` is somebody's complete reading history —
  the most personal file a feed reader has. `.gitignore` excludes `*.db`, `*.opml`, and the speech
  cache for exactly this reason. Back it up the way you would back up mail.
- **Secrets live in `.env`**, which is ignored; `.env.example` documents the shape. API keys, TLS
  material and anything matching the key/certificate patterns in `.gitignore` are excluded by
  extension rather than by name, because nobody commits a file called *secret* — they commit
  `server.key` at half past midnight.
- **Terminate TLS in front of it, or give it a certificate.** The gRPC tunnel is a WebSocket; over
  plain `ws://` on a public network it is plaintext. GoGRPCBridge supports `wss://` directly, and
  ships origin allowlists, pre-upgrade authorization, read limits, connection caps and upgrade rate
  limiting — they are worth configuring rather than defaulting.
- **Article HTML is sanitised before rendering**, and the client renders it into nodes rather than
  handing markup to the DOM raw. Feed content is hostile input by definition; treat any bypass of
  that path as a vulnerability.

## Supported versions

Pre-1.0. The most recent commit on `main` is the supported version, and there are no backports.
