//go:build js && wasm

package data

import (
	"context"
	"errors"
	"io"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The broadcast's music beds, pulled down the tunnel (§19).
//
// Every other large asset this client uses is a URL the browser fetches for
// itself. These are not, deliberately — see the RPC's own comment in
// system.proto — and the consequence lands here: the whole track arrives as a
// sequence of messages, is assembled in wasm memory, and is handed to an <audio>
// element as a blob. There is no streaming, no seeking and no partial start.
//
// So the two things this file owes the caller are a size ceiling and an honest
// error, because a four megabyte download over a shared WebSocket has more ways
// to go wrong than a GET does.

// AudioTrack is one bed, as the settings picker needs it.
type AudioTrack struct {
	ID    string
	Title string
	Bytes int64
	// Role is "bed" (low music under the whole broadcast) or "sting" (the louder
	// piece that opens it). Passed through unexamined: the server decides, and a
	// value this client does not recognise is treated as a bed by the caller.
	Role string
}

// maxTrackBytes is the most this client will assemble in memory.
//
// Twelve megabytes: comfortably above the largest bed anybody has shipped and
// far below anything that would matter in a tab already holding a thirty
// megabyte module. It exists because the length of a server stream is the
// server's choice, and "grow a byte slice until the server stops" is a shape
// that should never be written without a ceiling beside it.
const maxTrackBytes = 12 << 20

// ErrTrackTooBig is returned when a track exceeds maxTrackBytes.
//
// Its own error rather than a generic one, because the caller's remedy differs:
// a transport failure is worth retrying and this is not.
var ErrTrackTooBig = errors.New("data: that track is larger than this client will hold")

// ListAudioTracks names the beds this instance ships.
//
// An empty list is a normal answer — a deployment without the audio directory
// has none — and the caller renders a picker offering silence and nothing else
// rather than an error.
func (c *Client) ListAudioTracks(ctx context.Context) ([]AudioTrack, error) {
	if c == nil || c.system == nil {
		return nil, nil
	}
	res, err := c.system.ListAudioTracks(ctx, &pb.ListAudioTracksRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]AudioTrack, 0, len(res.GetTracks()))
	for _, t := range res.GetTracks() {
		if t.GetId() == "" {
			continue
		}
		out = append(out, AudioTrack{
			ID: t.GetId(), Title: t.GetTitle(), Bytes: t.GetBytes(), Role: t.GetRole(),
		})
	}
	return out, nil
}

// GetAudioTrack pulls one bed and returns its bytes.
//
// Blocking, and expected to be called from a goroutine: it holds a stream open
// for as long as the track takes to arrive, and doing that on the JS stack would
// freeze the tab for the duration (8b.35 — the tunnel's reply lands on the same
// loop, so a blocking wait there cannot succeed, only time out).
func (c *Client) GetAudioTrack(ctx context.Context, id string) ([]byte, error) {
	if c == nil || c.system == nil {
		return nil, errors.New("data: not connected")
	}
	stream, err := c.system.GetAudioTrack(ctx, &pb.GetAudioTrackRequest{Id: id})
	if err != nil {
		return nil, err
	}
	var out []byte
	for {
		msg, rerr := stream.Recv()
		if rerr == io.EOF {
			return out, nil
		}
		if rerr != nil {
			// Partial audio is worse than none: an <audio> element handed half a
			// file plays half a file and then stops, which reads as the feature
			// being broken rather than as a download that failed.
			return nil, rerr
		}
		out = append(out, msg.GetData()...)
		if len(out) > maxTrackBytes {
			return nil, ErrTrackTooBig
		}
	}
}
