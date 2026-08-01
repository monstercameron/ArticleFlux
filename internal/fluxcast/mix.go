package fluxcast

import "strconv"

// Channel is one of the three things that can make a sound.
//
// Two music channels rather than one, and that is a mixing decision rather than
// an implementation detail: the opening is a piece with a front to it — loud,
// out of the way under the greeting, back up when it finishes, gone — while the
// bed is furniture that fades in under the first story and stays. Run on one
// channel the handover between them is a crossfade you cannot perform, because
// you would be fading a track out and the same element in.
type Channel string

const (
	// ChannelTheme is the opening, and only the opening. It plays once.
	ChannelTheme Channel = "theme"
	// ChannelBed is the music under the programme.
	ChannelBed Channel = "bed"
	// ChannelVoice is the narrator. Never given a level here: speech is the
	// content and everything else is furniture that moves around it, so the
	// voice channel exists to be named in a trace rather than to be mixed.
	ChannelVoice Channel = "voice"
)

// MusicOp is what to do to a music channel.
type MusicOp string

const (
	// MusicStart begins a track, ramping up to Level over Over.
	MusicStart MusicOp = "start"
	// MusicLevel moves a playing channel to Level over Over. This is every
	// duck, lift and swell.
	MusicLevel MusicOp = "level"
	// MusicStop fades a channel out over Over and releases it.
	MusicStop MusicOp = "stop"
	// MusicOutro is the ending: up over Rise, then away over Over. One op
	// rather than two cues, because the two halves are one gesture and a
	// player that lost the second would leave music running under nothing.
	MusicOutro MusicOp = "outro"
	// MusicPause holds a channel where it is rather than fading it out, so
	// resuming picks the programme up instead of starting the track over.
	MusicPause MusicOp = "pause"
	// MusicResume releases a pause.
	MusicResume MusicOp = "resume"
)

// A MusicCue is one instruction to the mix, with the reason it exists.
//
// Why is not decoration. It is what the cue sheet prints and what a trace
// carries, and it is the difference between reading "bed → 0.036 over 800ms"
// and reading "the bed lifts so the seam sounds like the programme breathing".
// Every number in a mix is arbitrary until you know what it is for.
type MusicCue struct {
	Channel Channel
	Op      MusicOp
	// Track is the recording to play, for MusicStart. Chosen by the caller —
	// which piece is an opening and which is a bed is the server's answer, and
	// a piece written to sit under speech is not interchangeable with one
	// written to open a programme.
	Track string
	Level float64
	Over  Dur
	// Rise is MusicOutro's first half.
	Rise Dur
	Why  string
}

func (c MusicCue) String() string {
	s := string(c.Channel) + " " + string(c.Op)
	if c.Track != "" {
		s += " " + c.Track
	}
	switch c.Op {
	case MusicStart, MusicLevel:
		s += " → " + strconv.FormatFloat(c.Level, 'g', 3, 64) + " over " + c.Over.String()
	case MusicStop:
		s += " over " + c.Over.String()
	case MusicOutro:
		s += " ↑" + c.Rise.String() + " ↓" + c.Over.String()
	}
	return s
}

// ---------------------------------------------------------------------------
// The five gestures
// ---------------------------------------------------------------------------
//
// Every music move in the programme is one of these, built from the profile so
// that the numbers live in exactly one place. Each takes the profile rather than
// loose floats, because the relationships between them (a duck is relative to a
// level, a crossfade's two halves share one duration) are properties of the
// tuning, not of the call site — and the call site is where they drifted.

// openTheme is the first thing anybody hears.
func openTheme(p Profile, track string) MusicCue {
	return MusicCue{
		Channel: ChannelTheme, Op: MusicStart, Track: track,
		Level: p.Mix.StingOpen, Over: Secs(0.4),
		Why: "the programme starting, loud enough to be a piece of music",
	}
}

// duckTheme gets the theme out of the way of the greeting. Quick: music that
// takes two seconds to notice the narrator is two seconds of a listener
// straining.
func duckTheme(p Profile) MusicCue {
	return MusicCue{
		Channel: ChannelTheme, Op: MusicLevel,
		Level: p.Mix.StingOpen * p.Mix.StingUnderRatio, Over: p.Mix.StingDuck,
		Why: "under the greeting",
	}
}

// swellTheme brings it back when the greeting finishes. Slower than the duck,
// because coming back up is a musical gesture rather than a correction.
func swellTheme(p Profile) MusicCue {
	return MusicCue{
		Channel: ChannelTheme, Op: MusicLevel,
		Level: p.Mix.StingOpen, Over: p.Mix.StingSwell,
		Why: "the theme has the room to itself before the news",
	}
}

// crossToBed is the handover, and it is ONE gesture in two cues: the theme
// leaves over exactly the seconds the bed arrives in. Validate guarantees the
// two durations are equal, so a gap or an overlap in the middle of it is not
// something a tuning can express.
func crossToBed(p Profile, bedTrack string) (out, in MusicCue) {
	return MusicCue{
			Channel: ChannelTheme, Op: MusicStop, Over: p.Mix.StingOut,
			Why: "the theme leaves",
		}, MusicCue{
			Channel: ChannelBed, Op: MusicStart, Track: bedTrack,
			Level: p.Mix.BedLevel, Over: p.Mix.BedRise,
			Why: "the bed arrives underneath it, over the same seconds, so there is no silence between them",
		}
}

// duckBed and restBed are the narrator starting and stopping. Only six decibels
// apart, not the eleven a broadcast desk would use: this music is already far
// under the voice and its whole job is to be continuously there, so ducked hard
// it vanishes, which reads as the music having stopped rather than as ducking.
func duckBed(p Profile) MusicCue {
	return MusicCue{
		Channel: ChannelBed, Op: MusicLevel, Level: p.Mix.BedDucked, Over: p.Mix.BedFade,
		Why: "under the narrator",
	}
}

func restBed(p Profile) MusicCue {
	return MusicCue{
		Channel: ChannelBed, Op: MusicLevel, Level: p.Mix.BedLevel, Over: p.Mix.BedFade,
		Why: "back to resting",
	}
}

// seamBed is the lift between stories — above resting rather than at it,
// because a bed that merely stops ducking reads as a gap and one that comes up
// reads as the programme breathing. Quicker than an ordinary fade, since a seam
// is only a few seconds long and a lift that takes half of it has not happened.
func seamBed(p Profile) MusicCue {
	return MusicCue{
		Channel: ChannelBed, Op: MusicLevel, Level: p.Mix.BedSeam, Over: p.Mix.BedSwell,
		Why: "the seam: the programme breathing between stories",
	}
}

// outroBed ends it. The rise is quick and the fall is most of it, which is the
// shape of every piece of music that has ever ended.
func outroBed(p Profile) MusicCue {
	return MusicCue{
		Channel: ChannelBed, Op: MusicOutro,
		Level: p.Mix.BedSeam, Rise: p.Mix.OutroRise, Over: p.Mix.OutroFall,
		Why: "the programme closing",
	}
}

// stopAll is what leaving does. Not a fade: somebody who shut the show wants it
// gone, and music that outlives the screen is the worst bug this feature could
// have.
func stopAll() []MusicCue {
	return []MusicCue{
		{Channel: ChannelTheme, Op: MusicStop, Over: Secs(0.25), Why: "the show was left"},
		{Channel: ChannelBed, Op: MusicStop, Over: Secs(0.25), Why: "the show was left"},
	}
}
