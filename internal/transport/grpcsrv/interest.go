package grpcsrv

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The interest profile at the wire edge (§18.2, §18.9).
//
// Translation only, in this file's usual sense, with one thing worth naming: the
// enum. `steer` is a multiplier in the database and a WORD on the wire, and the
// conversion in both directions lives here rather than in the client or the
// store. A client sending 1.5 would be deciding what "more" is worth, which is a
// scoring judgement that has to stay revisable without a client deploy.

func (s *ReaderServer) GetInterestProfile(ctx context.Context, _ *pb.GetInterestProfileRequest) (*pb.GetInterestProfileResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	p, err := s.svc.InterestProfile(ctx, sc)
	if err != nil {
		return nil, toStatus(err)
	}

	out := &pb.GetInterestProfileResponse{
		RankedCount: int32(p.Ranked),
		FactorBase:  int32(p.FactorBase),
		ColdStart:   p.ColdStart,
		TopicCount:  int32(p.TopicCount),
		EntityCount: int32(p.EntityCount),
		FeedCount:   int32(p.FeedCount),
	}
	for _, t := range p.Topics {
		out.Topics = append(out.Topics, &pb.InterestTopic{
			Id:          t.ID,
			Label:       t.Label,
			LabelByUser: t.LabelSource == "user",
			Terms:       t.TopTerms,
			MemberCount: int32(t.MemberCount),
			Trend:       t.Trend,
			Level:       toPBSteer(t.Steer, t.Suppressed),
			// The handle the screen steers with. See InterestTopic.key: the id
			// beside it is this derivation's, and the next one will not agree.
			Key: store.TopicKey(t.TopTerms),
		})
	}
	for _, e := range p.Entities {
		out.Entities = append(out.Entities, &pb.InterestEntity{
			Name:        e.Name,
			Label:       e.Label,
			Kind:        e.Kind,
			Weight:      e.Weight,
			Mentions:    int32(e.Mentions),
			Level:       toPBSteer(e.Steer, e.Suppressed),
			FirstSeenAt: e.FirstSeen,
			LastSeenAt:  e.LastSeen,
		})
	}
	for _, f := range p.Feeds {
		out.Feeds = append(out.Feeds, &pb.InterestFeed{
			SourceId:     f.SourceID,
			Title:        f.Title,
			Score:        f.Score,
			Opens:        int32(f.Opens),
			Impressions:  int32(f.Impressions),
			VolumePerDay: f.VolumePerDay,
			OnMyFeed:     f.OnMyFeed,
		})
	}
	for _, fa := range p.Factors {
		out.Factors = append(out.Factors, &pb.InterestFactor{
			Term: fa.Term, Items: int32(fa.Items),
		})
	}
	return out, nil
}

func (s *ReaderServer) SteerInterest(ctx context.Context, req *pb.SteerInterestRequest) (*pb.SteerInterestResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}

	topic, entity := req.GetTopicKey(), req.GetEntityName()
	// Exactly one target. Both, or neither, is a request that means two things or
	// nothing — and guessing at either is how a correction lands on the wrong row
	// and teaches the reader the control does not work.
	if (topic == "") == (entity == "") {
		return nil, status.Error(codes.InvalidArgument,
			"steer: give exactly one of topic_key or entity_name")
	}
	level, ok := fromPBSteer(req.GetLevel())
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "steer: no level given")
	}

	var rebuilding bool
	if topic != "" {
		rebuilding, err = s.svc.SteerTopic(ctx, sc, topic, level)
	} else {
		rebuilding, err = s.svc.SteerEntity(ctx, sc, entity, level)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// A cluster the reader's engagement no longer produces, or an
			// entity that is not theirs. NotFound rather than InvalidArgument:
			// the request was well formed, and a cluster genuinely can retire
			// between the screen loading and the button being pressed.
			//
			// This is now an honest answer rather than the routine one it was
			// while topics were addressed by row id — see reader.SteerTopic.
			return nil, toStatus(err)
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.SteerInterestResponse{Rebuilding: rebuilding}, nil
}

// toPBSteer turns the stored pair into the level the screen shows.
//
// Suppressed wins over the multiplier, which is not merely a precedence rule: a
// suppressed row keeps whatever dial it had before, so a reader who set "more"
// and then "never" must see NEVER rather than the louder of their two answers.
func toPBSteer(steer float64, suppressed bool) pb.SteerLevel {
	if suppressed {
		return pb.SteerLevel_STEER_LEVEL_NEVER
	}
	switch {
	case steer >= store.SteerMore:
		return pb.SteerLevel_STEER_LEVEL_MORE
	case steer <= store.SteerLess:
		return pb.SteerLevel_STEER_LEVEL_LESS
	}
	return pb.SteerLevel_STEER_LEVEL_NORMAL
}

// fromPBSteer is the inverse. UNSPECIFIED is refused rather than defaulted to
// normal: a client that forgot the field is asking to reset a dial it did not
// mean to touch.
func fromPBSteer(l pb.SteerLevel) (reader.SteerLevel, bool) {
	switch l {
	case pb.SteerLevel_STEER_LEVEL_MORE:
		return reader.SteerMore, true
	case pb.SteerLevel_STEER_LEVEL_NORMAL:
		return reader.SteerNormal, true
	case pb.SteerLevel_STEER_LEVEL_LESS:
		return reader.SteerLess, true
	case pb.SteerLevel_STEER_LEVEL_NEVER:
		return reader.SteerNever, true
	}
	return "", false
}
