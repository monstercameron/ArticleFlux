package grpcsrv

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The broadcast's music beds, served down the tunnel (§19).
//
// # Why these are not a static URL
//
// Every other large thing this server hands a browser is a URL: the wasm module,
// the fonts, the icons, the proxied images, and — argued at length in §10.7 —
// the spoken audio, because an <audio src> lets the browser stream, seek, buffer
// and cache without any of it costing us code.
//
// The beds go the other way, and the reason is not a technical one: they are
// part of the APPLICATION rather than part of anyone's reading, and keeping them
// on the one authenticated connection means a deployment has a single door to
// reason about instead of a static path beside it.
//
// The cost is real and is stated in the proto so it is visible from the
// contract: no range requests, no partial playback, the whole track in the
// client's memory, and a few seconds before the first note. Chunked so the
// tunnel stays usable while it arrives.
//
// # Why the catalogue is a table and not a directory listing
//
// A slug that reaches the filesystem is a path traversal waiting to happen, and
// "sanitise it carefully" is the version of that sentence which is wrong once.
// The id from the wire is matched against THIS list and never joined onto
// anything; a request for an id that is not here is NotFound before any path
// exists. The filename beside it is a constant in this file.
//
// The titles are their author's, and are proper nouns rather than copy: they are
// not translated, and they do not go through the i18n catalogue on either side.
var audioBeds = []struct{ id, title, file string }{
	{"signal-and-ideas", "Signal and Ideas", "signal-and-ideas.mp3"},
	{"midnight-thought-loop", "Midnight Thought Loop", "midnight-thought-loop.mp3"},
	{"late-night-patchcord", "Late Night Patchcord", "late-night-patchcord.mp3"},
	// A second take of the same piece, kept because it is a different recording
	// rather than a duplicate — shorter, and it sits differently under speech.
	{"late-night-patchcord-ii", "Late Night Patchcord II", "late-night-patchcord-ii.mp3"},
}

// audioChunk is how much of a track goes in one message.
//
// 64KiB, and the number is about the TUNNEL rather than about throughput. Every
// RPC this client makes shares one WebSocket, so a four megabyte track sent as
// one message would park the item list, the read marks and the note autosave
// behind it for as long as it took to arrive. Sixty-four kilobyte frames
// interleave with everything else and the reader never notices the download.
const audioChunk = 64 << 10

// WithAudio attaches the directory the beds live in.
//
// Optional and separate from the constructor, like every other capability on
// these servers: an instance that shipped without the audio directory answers
// ListAudioTracks with an empty list, and the settings picker then offers
// silence and nothing else. Background music is not a wiring error.
func (s *SystemServer) WithAudio(dir string) *SystemServer {
	s.audioDir = dir
	return s
}

// ListAudioTracks names the beds this instance actually has on disk.
//
// Stat'd rather than assumed: a deployment that copied the binary and forgot the
// web root would otherwise offer four tracks and fail on all of them, which is a
// worse answer than offering none.
func (s *SystemServer) ListAudioTracks(_ context.Context, _ *pb.ListAudioTracksRequest) (
	*pb.ListAudioTracksResponse, error) {
	out := &pb.ListAudioTracksResponse{}
	if s.audioDir == "" {
		return out, nil
	}
	for _, b := range audioBeds {
		fi, err := os.Stat(filepath.Join(s.audioDir, b.file))
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			continue
		}
		out.Tracks = append(out.Tracks, &pb.AudioTrack{
			Id: b.id, Title: b.title, Bytes: fi.Size(),
		})
	}
	return out, nil
}

// GetAudioTrack streams one bed.
//
// The id is matched against audioBeds and never used to build a path, so this
// cannot be pointed at a file the table does not name — which is the whole
// reason the table exists.
func (s *SystemServer) GetAudioTrack(req *pb.GetAudioTrackRequest,
	stream pb.SystemService_GetAudioTrackServer) error {
	if s.audioDir == "" {
		return status.Error(codes.NotFound, "this server ships no music")
	}
	name := ""
	for _, b := range audioBeds {
		if b.id == req.GetId() {
			name = b.file
			break
		}
	}
	if name == "" {
		return status.Error(codes.NotFound, "no such track")
	}

	f, err := os.Open(filepath.Join(s.audioDir, name))
	if err != nil {
		// The path is ours and the id was matched, so this is a deployment that
		// lost its files rather than anything the caller did.
		return status.Error(codes.NotFound, "that track is not on this server")
	}
	defer f.Close()

	buf := make([]byte, audioChunk)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			// Send a COPY. The proto message holds the slice, and gRPC may still
			// be marshalling it when the next Read overwrites the buffer — a bug
			// that produces audio which is subtly, intermittently wrong rather
			// than audio that fails.
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if serr := stream.Send(&pb.GetAudioTrackResponse{Data: chunk}); serr != nil {
				// The client went away mid-track, which is ordinary: they turned
				// the bed off, or left. Not an error worth reporting upward.
				return serr
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return status.Error(codes.Internal, "could not read that track")
		}
	}
}
