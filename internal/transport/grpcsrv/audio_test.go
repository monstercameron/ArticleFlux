package grpcsrv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The music beds (§19).
//
// Two properties are worth a test and the rest is plumbing: an id from the wire
// must never reach the filesystem, and the roles must match the recordings — a
// piece written to open a programme played as background is a mix nobody can
// hear the news over, and the client picks by role alone.

// sendAll collects a server stream into one slice.
type trackSink struct {
	pb.SystemService_GetAudioTrackServer
	got []byte
}

func (s *trackSink) Send(res *pb.GetAudioTrackResponse) error {
	s.got = append(s.got, res.GetData()...)
	return nil
}

func (s *trackSink) Context() context.Context { return context.Background() }

// audioDir writes the named files with recognisable contents.
func audioDir(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("mp3:"+f), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return dir
}

// Every track declares a role, and the two Patchcord takes are the beds. That
// pairing is the mix: they were recorded to sit under a voice, and the other two
// were not.
func TestEveryTrackHasARoleAndThePatchcordsAreBeds(t *testing.T) {
	for _, b := range audioBeds {
		switch b.role {
		case audioBed, audioSting:
		default:
			t.Errorf("%q has role %q, which is neither", b.id, b.role)
		}
		if !strings.HasSuffix(b.file, ".mp3") {
			t.Errorf("%q names %q", b.id, b.file)
		}
		if strings.ContainsAny(b.file, `/\`) {
			t.Errorf("%q names a path rather than a file: %q", b.id, b.file)
		}
		if want := audioBed; strings.HasPrefix(b.id, "late-night-patchcord") && b.role != want {
			t.Errorf("%q is a %q, wanted a %q", b.id, b.role, want)
		}
	}
	// At least one of each, or half the choreography has nothing to play.
	var beds, stings int
	for _, b := range audioBeds {
		if b.role == audioBed {
			beds++
		} else {
			stings++
		}
	}
	if beds == 0 || stings == 0 {
		t.Errorf("%d beds and %d openings — the broadcast needs both", beds, stings)
	}
}

// The listing is what is ON DISK, with the role attached. A deployment that
// copied the binary and forgot the web root offers nothing rather than four
// tracks that all fail.
func TestListAudioTracksReportsWhatExists(t *testing.T) {
	s := &SystemServer{}
	if res, err := s.ListAudioTracks(context.Background(), &pb.ListAudioTracksRequest{}); err != nil ||
		len(res.GetTracks()) != 0 {
		t.Fatalf("a server with no audio directory answered %v, %v", res, err)
	}

	s = (&SystemServer{}).WithAudio(audioDir(t, audioBeds[0].file, audioBeds[2].file))
	res, err := s.ListAudioTracks(context.Background(), &pb.ListAudioTracksRequest{})
	if err != nil {
		t.Fatalf("ListAudioTracks: %v", err)
	}
	if len(res.GetTracks()) != 2 {
		t.Fatalf("listed %d tracks, want the 2 on disk", len(res.GetTracks()))
	}
	for _, tr := range res.GetTracks() {
		if tr.GetRole() == "" {
			t.Errorf("%q was listed with no role — the client cannot place it", tr.GetId())
		}
		if tr.GetBytes() == 0 {
			t.Errorf("%q was listed as empty", tr.GetId())
		}
		if tr.GetTitle() == "" {
			t.Errorf("%q was listed with no name", tr.GetId())
		}
	}
}

// The id is matched against the table and never joined onto a path. This is the
// whole reason the table exists.
func TestGetAudioTrackRefusesAnythingNotInTheTable(t *testing.T) {
	s := (&SystemServer{}).WithAudio(audioDir(t, audioBeds[0].file))

	for _, id := range []string{
		"", "nope",
		"../../../etc/passwd",
		`..\..\windows\win.ini`,
		audioBeds[0].file, // the FILENAME is not an id
	} {
		err := s.GetAudioTrack(&pb.GetAudioTrackRequest{Id: id}, &trackSink{})
		if status.Code(err) != codes.NotFound {
			t.Errorf("id %q returned %v, want NotFound", id, err)
		}
	}

	// The one that is in the table and on disk comes back whole.
	sink := &trackSink{}
	if err := s.GetAudioTrack(&pb.GetAudioTrackRequest{Id: audioBeds[0].id}, sink); err != nil {
		t.Fatalf("GetAudioTrack: %v", err)
	}
	if got, want := string(sink.got), "mp3:"+audioBeds[0].file; got != want {
		t.Errorf("streamed %q, want %q", got, want)
	}

	// In the table but not on disk: still NotFound, and never a panic.
	sink = &trackSink{}
	if err := s.GetAudioTrack(&pb.GetAudioTrackRequest{Id: audioBeds[1].id}, sink); status.Code(err) != codes.NotFound {
		t.Errorf("a track missing from disk returned %v, want NotFound", err)
	}
}

// A track larger than one message arrives in one piece. The copy inside the send
// loop is what makes this true, and without it the audio is subtly wrong rather
// than absent — the kind of bug that gets blamed on the encoder.
func TestGetAudioTrackReassemblesExactly(t *testing.T) {
	dir := t.TempDir()
	body := make([]byte, audioChunk*2+1234)
	for i := range body {
		body[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(dir, audioBeds[0].file), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := (&SystemServer{}).WithAudio(dir)

	sink := &trackSink{}
	if err := s.GetAudioTrack(&pb.GetAudioTrackRequest{Id: audioBeds[0].id}, sink); err != nil {
		t.Fatalf("GetAudioTrack: %v", err)
	}
	if len(sink.got) != len(body) {
		t.Fatalf("streamed %d bytes of %d", len(sink.got), len(body))
	}
	for i := range body {
		if sink.got[i] != body[i] {
			t.Fatalf("byte %d differs: got %d, want %d", i, sink.got[i], body[i])
		}
	}
}
