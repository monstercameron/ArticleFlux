-- 0031_recommendation_topic — "steer by rejection" (Cam, 2026-08-01; §18.7).
--
-- A dismissal already blocks its own domain forever (recommendations.status =
-- 'dismissed', read back by DismissedDomains) — that part needed no schema
-- change. What it could not do is teach the scorer anything about the KIND of
-- site to stop proposing, because the topic a candidate matched was never
-- persisted past the evidence sentence it was folded into. This column is
-- that missing fact, stored structurally rather than re-parsed out of prose,
-- so DismissedTopics can count "how many sites in this topic has the reader
-- already said no to" without touching the LLM boundary at all — the
-- steering this enables happens entirely in internal/recommend's local
-- scoring, never in what reaches internal/llm/relevance.go (§18.8).
ALTER TABLE recommendations ADD COLUMN topic_label TEXT;

-- Sparse by design, same reasoning as uis_user_heard (0030): most
-- recommendations come from rungs 1-2 (outlink/aggregator mining), which
-- carry no topic label at all, and the common row never touches this index.
CREATE INDEX recommendations_dismissed_topic ON recommendations(user_id, topic_label)
    WHERE status = 'dismissed' AND topic_label IS NOT NULL;
