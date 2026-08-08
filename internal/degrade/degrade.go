// Package degrade decides what to stop doing as the disk fills
// (TODO 6.13, plan.md §22.6).
//
// # Why a ladder rather than an alert
//
// `content_html`, extractions, archives, embeddings, engagements, packs,
// revisions and generated audio all grow monotonically. A full disk on SQLite is
// `SQLITE_FULL` in the middle of a write — not a graceful stop, and not
// something the reader can be told about usefully, because telling them is also
// a write.
//
// So the failure is staged, and the order is the whole design: shed the
// regenerable things first and the irreplaceable things last, or never.
//
//	< 20%  stop new audio, snapshots and renders; warn
//	< 10%  stop packs and image caching; sweep archives; notify
//	<  5%  read-only for INGEST — polling pauses, but reading, marking read,
//	       notes and the sync API keep working
//	<  2%  refuse every write but the outbox drain
//
// The 5% rung is the one worth reading twice. Pausing ingest while keeping
// reading, marking-read and notes alive is not a compromise, it is the correct
// priority: new articles are re-fetchable from the publisher, and a note is not.
// An instance that stopped accepting notes to keep polling would be shedding the
// only irreplaceable thing it holds in order to protect the most replaceable.
//
// # Pure, and therefore testable at every rung
//
// This package computes a policy from a number. It does not stat the disk, take
// a lock, or stop anything — the caller supplies free space and asks what is
// allowed. A degrade ladder that can only be exercised by filling a real disk is
// a degrade ladder nobody has ever seen run.
//
// # NOT WIRED. Nothing imports this package.
//
// Every function here — TierFor, Allowed, SweepTarget, Banner, Explain — has
// zero callers outside its own tests. The ladder above describes a policy the
// instance does not currently follow, and recording that is worth more than
// leaving it to be discovered: internal/retention/security.go states the rule
// this note exists under, that "a documented schedule that does not exist is
// worse than an admitted absence, because it is the sentence somebody reads when
// deciding whether the table needs attention."
//
// What actually protects the disk today is internal/app/diskhealth.go, and it is
// a different shape in both axes:
//
//   - It measures an ABSOLUTE floor, `diskFloor = 256 MB`, not a fraction. On a
//     1 TB volume that is 0.025%, so the first four rungs above never fire; on a
//     25 GB droplet it is about 1%, which lands between this ladder's Frozen and
//     ReadOnlyIngest rungs.
//   - It is BINARY. `diskReady` either permits writes or refuses them. There is
//     no warn rung, no shedding of packs or image caching, no SweepTarget, and
//     none of the four banners below reaches a reader. (`sweepCaches` does bound
//     the on-disk caches, but against its own budget rather than against free
//     space.)
//
// So the live behaviour is the one Banner's comment names as the thing to avoid:
// "an application that degrades silently and then refuses a write is one where
// the first sign of trouble is a failure, and by then the operator has no
// runway."
//
// Wiring it is a real piece of work — the capability table has to be threaded
// through the call sites that can be shed, and the banner needs a surface — so
// this note is not a plan. It is so that the next person to read the ladder
// knows it is a design and not a description.
package degrade

import "fmt"

// Tier is a rung of the ladder.
type Tier int

const (
	// Normal is everything permitted.
	Normal Tier = iota
	// Warn is under 20%: stop the expensive optional work.
	Warn
	// Shed is under 10%: stop caching and sweep what can be regenerated.
	Shed
	// ReadOnlyIngest is under 5%: polling pauses, reading continues.
	ReadOnlyIngest
	// Frozen is under 2%: refuse every write but the outbox drain.
	Frozen
)

func (t Tier) String() string {
	switch t {
	case Normal:
		return "normal"
	case Warn:
		return "warn"
	case Shed:
		return "shed"
	case ReadOnlyIngest:
		return "read-only-ingest"
	case Frozen:
		return "frozen"
	}
	return "unknown"
}

// Capability is something the application might do.
type Capability int

const (
	// CapAudio is TTS synthesis: tens of megabytes per article, and the first
	// thing to go because it is pure luxury and fully regenerable.
	CapAudio Capability = iota
	// CapSnapshot is tier-2 page snapshots.
	CapSnapshot
	// CapRender is §10.1c's new renders.
	CapRender
	// CapPackBuild is offline pack building.
	CapPackBuild
	// CapImageCache is the asset proxy's image cache.
	CapImageCache
	// CapArchive is preservation writes.
	CapArchive
	// CapPoll is fetching feeds — new content coming in.
	CapPoll
	// CapEmbed is Smart+ embedding generation.
	CapEmbed
	// CapReadState is marking read, starring, rating.
	CapReadState
	// CapNotes is writing notes and tags: the irreplaceable data (R8).
	CapNotes
	// CapOutboxDrain is applying an offline client's queued mutations. Never
	// refused: those writes already happened on a device and refusing them
	// destroys work the reader believes is saved.
	CapOutboxDrain
)

func (c Capability) String() string {
	switch c {
	case CapAudio:
		return "audio synthesis"
	case CapSnapshot:
		return "page snapshots"
	case CapRender:
		return "renders"
	case CapPackBuild:
		return "pack building"
	case CapImageCache:
		return "image caching"
	case CapArchive:
		return "archiving"
	case CapPoll:
		return "polling"
	case CapEmbed:
		return "embeddings"
	case CapReadState:
		return "read state"
	case CapNotes:
		return "notes"
	case CapOutboxDrain:
		return "outbox drain"
	}
	return "unknown"
}

// State is the disk situation.
type State struct {
	FreeBytes  int64
	TotalBytes int64
}

// FreeFraction is 0..1. Returns 1 when total is unknown, so a stat failure
// degrades to "assume fine" rather than to "freeze everything" — an instance
// that froze because it could not read a filesystem would be unusable for a
// reason nobody could see.
func (s State) FreeFraction() float64 {
	if s.TotalBytes <= 0 {
		return 1
	}
	return float64(s.FreeBytes) / float64(s.TotalBytes)
}

// TierFor maps free space to a rung.
func TierFor(s State) Tier {
	f := s.FreeFraction()
	switch {
	case f < 0.02:
		return Frozen
	case f < 0.05:
		return ReadOnlyIngest
	case f < 0.10:
		return Shed
	case f < 0.20:
		return Warn
	default:
		return Normal
	}
}

// Allowed reports whether a capability may run at a tier.
//
// The table is written out rather than computed from an ordering, because the
// ordering is the decision and burying it in `cap >= tier` arithmetic would make
// it invisible — and it is the part of this package anyone reviewing it should
// actually read.
func Allowed(t Tier, c Capability) bool {
	// Never refused, at any tier. These writes already happened on a device and
	// refusing them destroys work the reader believes is saved.
	if c == CapOutboxDrain {
		return true
	}

	switch t {
	case Normal:
		return true

	case Warn:
		// The expensive optional work. Audio is tens of megabytes an article.
		switch c {
		case CapAudio, CapSnapshot, CapRender:
			return false
		}
		return true

	case Shed:
		switch c {
		case CapAudio, CapSnapshot, CapRender, CapPackBuild, CapImageCache, CapEmbed:
			return false
		}
		return true

	case ReadOnlyIngest:
		// Polling stops. Reading, marking read and notes do not — new articles
		// are re-fetchable from the publisher and a note is not, so shedding
		// ingest to protect notes is the correct priority rather than a
		// compromise.
		switch c {
		case CapReadState, CapNotes:
			return true
		}
		return false

	case Frozen:
		// Everything but the outbox drain, handled above.
		return false
	}
	return false
}

// Explain gives the reason a capability is refused, for a banner or an API
// error.
//
// §22.6 asks for failure that is "graceful and legible rather than opaque", and
// legible means the message says what is wrong and what will fix it. "Write
// failed" says neither.
func Explain(t Tier, c Capability) string {
	if Allowed(t, c) {
		return ""
	}
	switch t {
	case Warn:
		return fmt.Sprintf("%s is paused: under 20%% disk free", c)
	case Shed:
		return fmt.Sprintf("%s is paused: under 10%% disk free, and old archives are being swept", c)
	case ReadOnlyIngest:
		return fmt.Sprintf("%s is paused: under 5%% disk free. Reading, marking read and notes still work", c)
	case Frozen:
		return fmt.Sprintf("%s is refused: under 2%% disk free. Free space urgently", c)
	}
	return fmt.Sprintf("%s is unavailable", c)
}

// SweepTarget is how many bytes to try to reclaim at a tier.
//
// Zero below the Shed rung: sweeping when there is plenty of space is deleting
// things nobody asked to delete. Expressed as a fraction of total rather than an
// absolute, because a 20 GB VPS and a 2 TB box need different numbers and one
// constant would be wrong on both.
func SweepTarget(s State) int64 {
	switch TierFor(s) {
	case Shed:
		return s.TotalBytes / 100 // 1%
	case ReadOnlyIngest:
		return s.TotalBytes / 50 // 2%
	case Frozen:
		return s.TotalBytes / 25 // 4%
	default:
		return 0
	}
}

// Banner is the message shown to every user at a tier, or "" for none.
//
// Shown from the Warn rung rather than only when something breaks. An
// application that degrades silently and then refuses a write is one where the
// first sign of trouble is a failure, and by then the operator has no runway.
func Banner(t Tier) string {
	switch t {
	case Warn:
		return "Disk space is low. Audio and snapshots are paused."
	case Shed:
		return "Disk space is low. Pack building and image caching are paused, and old archives are being removed."
	case ReadOnlyIngest:
		return "Disk space is critical. New articles are not being fetched. Reading, marking read and notes still work."
	case Frozen:
		return "Disk space is exhausted. The instance is read-only until space is freed."
	default:
		return ""
	}
}
