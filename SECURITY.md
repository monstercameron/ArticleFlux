# Security

ArticleFlux is a self-hosted, multi-tenant reader. Two properties matter more than anything else in it,
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

Every IPv6 spelling of an IPv4 address is unwrapped before the deny lists are consulted — mapped
(`::ffff:127.0.0.1`), compatible (`::127.0.0.1`) and NAT64 (`64:ff9b::7f00:1`). Go's `To4` only knows
the first, so the other two used to match no rule at all: `::169.254.169.254` reached the metadata
endpoint under *every* policy, and the NAT64 form reached it whenever `-allow-private` was set, which
`-dev` sets automatically. Unwrapping rather than blocking each prefix is what keeps a wrapped
loopback following the same policy as a bare one.

If you are adding an outbound call, it goes through the guard. There is no fast path.

### The dev server's missing login is an explicit flag, never an inference

`./scripts/make.ps1 dev` serves without authentication so that the loop stays short. It is opt-in via
`-dev`, defaults to off, and is refused on anything but a loopback bind *and* refused when
`-behind-proxy` is set.

It used to be derived from the bind address alone, and that was the single most dangerous line in the
program. A loopback bind cannot be reached from outside the *machine*, which is true of the socket
and false of the *deployment*: every reverse-proxy setup, including the nginx config in `deploy/`,
terminates TLS on :443 and forwards to 127.0.0.1:9000. Under the old rule, the canonical way to host
this was also the way to publish somebody's entire reading history to anyone who typed the domain.

A bind address is a fact about network topology. It cannot tell you who is on the other end of a
connection, and nothing that cannot tell you that may decide whether to ask for a password. The
loopback check is still there as a second condition, and `-behind-proxy` is a third — an operator
stating that something forwards to this process, which is exactly the fact the bind address cannot
supply. A stale `.env` setting `ARTICLEFLUX_DEV` on a server therefore fails to start rather than
quietly serving an open reader.

### The app document carries a content security policy

The session token lives in `localStorage` rather than a cookie, because it travels as a gRPC metadata
header the browser never attaches by itself — which means there is no CSRF surface. The cost of that
trade is that the token has no `HttpOnly` to hide behind, so script execution on the app's origin is
total account compromise.

`internal/sanitize` is what stops feed content from becoming script, and a sanitiser is one bug away
from being nothing. So the shell is served under `default-src 'self'` with the boot script allowed by
**hash** rather than `'unsafe-inline'`, `object-src`/`base-uri`/`form-action`/`frame-ancestors` set to
`'none'`, and `connect-src 'self'` — which is the directive that decides where a compromised page
could send the token. The hash is computed from `web/index.html` at boot, so editing the shell cannot
leave a stale policy behind.

### The client fetches nothing from a third party

The shell loaded three font families from `fonts.googleapis.com`, which told Google the reader's IP
address and that they had opened the app, on every page load, before they had read anything. No
setting turned it on, because it arrived as a typography decision rather than a network one.

Fonts are self-hosted in `web/fonts/`. `default-src 'self'` is what keeps it that way, and a test
asserts the shell references no third-party host. If you add an external resource, you are changing
the privacy boundary at the top of this document.

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

- **The database is the sensitive artefact.** `articleflux.db` is somebody's complete reading history —
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
