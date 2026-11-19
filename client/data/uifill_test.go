package data

import (
	"context"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

func noSleep(context.Context, time.Duration) bool { return true }

func TestTheFillLoopAsksOnlyForMissingKeysAndStopsWhenDone(t *testing.T) {
	const loc = "hi"
	en, _ := i18n.English("login.submit")
	var asked [][]string
	var fills []bool
	round := 0
	fetch := func(_ context.Context, req *pb.GetUIStringsRequest) (*pb.GetUIStringsResponse, error) {
		asked = append(asked, req.GetKeys())
		fills = append(fills, req.GetFill())
		round++
		if round == 1 {
			// Nothing yet, but a fill is running.
			return &pb.GetUIStringsResponse{Locale: loc, Pending: int32(len(req.GetKeys())), Filling: true}, nil
		}
		// The fill has landed one key; the rest the server left in English.
		return &pb.GetUIStringsResponse{Locale: loc, Messages: []*pb.UIMessage{{
			Key: "login.submit", Text: "साइन इन करें", SourceHash: i18n.SourceHash(en),
		}}}, nil
	}
	merged := 0
	res, err := runUIFill(context.Background(), fetch, loc, true, func(n int) { merged = n }, noSleep)
	if err != nil {
		t.Fatal(err)
	}
	if round != 2 || res.Applied != 1 || merged != 1 || res.Pending != 0 {
		t.Fatalf("rounds=%d result=%+v merged=%d", round, res, merged)
	}
	if !fills[0] || fills[1] {
		t.Errorf("fill flags %v: only the first round may ask the server to spend", fills)
	}
	if len(asked[0]) != len(i18n.Export(i18n.DefaultLocale)) {
		t.Errorf("first round asked for %d keys, want every key", len(asked[0]))
	}
	if got := i18n.At(loc).T("login", "submit"); got != "साइन इन करें" {
		t.Errorf("the merged key did not render: %q", got)
	}
	// And the next fill asks for one key fewer.
	asked = nil
	round = 1
	_, _ = runUIFill(context.Background(), fetch, loc, false, nil, noSleep)
	if len(asked[0]) != len(i18n.Export(i18n.DefaultLocale))-1 {
		t.Errorf("second fill asked for %d keys; the merged one should be gone", len(asked[0]))
	}
}

func TestTheFillLoopDropsAStaleOrBrokenTranslation(t *testing.T) {
	const loc = "vi"
	fetch := func(_ context.Context, req *pb.GetUIStringsRequest) (*pb.GetUIStringsResponse, error) {
		return &pb.GetUIStringsResponse{Locale: loc, Messages: []*pb.UIMessage{
			// Made from a different English than this bundle has.
			{Key: "login.submit", Text: "Đăng nhập", SourceHash: "another-build"},
			// Lost its placeholder.
			{Key: "smart.langFailed", Text: "Lỗi"},
		}}, nil
	}
	res, err := runUIFill(context.Background(), fetch, loc, false, nil, noSleep)
	if err != nil || res.Applied != 0 {
		t.Fatalf("applied %d, err %v — both entries should have been refused", res.Applied, err)
	}
	en, _ := i18n.English("login.submit")
	if got := i18n.At(loc).T("login", "submit"); got != en.Text {
		t.Errorf("a refused entry rendered: %q, want the English %q", got, en.Text)
	}
}

func TestTheFillLoopStopsWhenTheServerWillNotFill(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, req *pb.GetUIStringsRequest) (*pb.GetUIStringsResponse, error) {
		calls++
		return &pb.GetUIStringsResponse{Locale: "th", Pending: 5, IdleReason: "not_owner"}, nil
	}
	res, err := runUIFill(context.Background(), fetch, "th", true, nil, noSleep)
	if err != nil || calls != 1 || res.IdleReason != "not_owner" || res.Pending != 5 {
		t.Fatalf("calls=%d res=%+v err=%v", calls, res, err)
	}
}

func TestTheFillLoopIsBoundedAndCancellable(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, req *pb.GetUIStringsRequest) (*pb.GetUIStringsResponse, error) {
		calls++
		return &pb.GetUIStringsResponse{Locale: "ko", Pending: 3, Filling: true}, nil
	}
	if _, err := runUIFill(context.Background(), fetch, "ko", true, nil, noSleep); err != nil {
		t.Fatal(err)
	}
	if calls != fillMaxRounds {
		t.Errorf("a wedged fill polled %d times, want the cap %d", calls, fillMaxRounds)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	_, err := runUIFill(ctx, fetch, "ko", true, nil, nil)
	if err == nil || calls != 1 {
		t.Errorf("a cancelled fill: calls=%d err=%v", calls, err)
	}
	if res, _ := runUIFill(context.Background(), fetch, i18n.DefaultLocale, true, nil, noSleep); res.Applied != 0 {
		t.Error("English was filled")
	}
}
