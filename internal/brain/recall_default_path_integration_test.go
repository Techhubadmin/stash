package brain_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alash3al/stash/internal/brain"
	"github.com/alash3al/stash/internal/db"
	"github.com/alash3al/stash/internal/queries"
	"github.com/pgvector/pgvector-go"
)

// TestRecallDefaultPathEpisodesCompete is the regression for episode starvation
// (system audit 2026-10-29, cross-engine review A2). With learning and provider
// reranking OFF, recall used to fetch facts up to the limit and give episodes only
// the leftover slots, so once facts filled the limit no episode could ever surface —
// everything written since consolidation stopped was unrecallable. Mutating
// `episodeLimit := limit` back to `limit - len(results)` must fail this test.
func TestRecallDefaultPathEpisodesCompete(t *testing.T) {
	dsn := os.Getenv("STASH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("STASH_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, dsn, "test-embedding", 3)
	if err != nil {
		t.Fatal(err)
	}
	q, err := queries.New()
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	cfg := brain.DefaultConfig()
	cfg.RetrievalLearningEnabled = false
	cfg.ProviderReranker = nil
	// Query vector is [1,0,0]; a stored vector's cosine score is its first component.
	br, err := brain.New(pool, fixedEmbedder{vector: []float32{1, 0, 0}}, noopReasoner{}, q, cfg)
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	defer br.Close()

	stamp := time.Now().UnixNano()
	slug := fmt.Sprintf("/recall-default-test-%d", stamp)
	otherSlug := fmt.Sprintf("/recall-default-other-%d", stamp)
	nsID, err := br.CreateNamespace(ctx, slug, "Recall default path test", "integration test")
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := br.CreateNamespace(ctx, otherSlug, "Recall default path other", "integration test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(ctx, "DELETE FROM namespaces WHERE id = ANY($1)", []int64{nsID, otherID}) }()

	now := time.Now().UTC()
	insertFact := func(ns int64, content string, vector []float32, confidence float32) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO facts (namespace_id, content, embedding, embedding_model, confidence, valid_from)
			 VALUES ($1, $2, $3, 'test-embedding', $4, $5) RETURNING id`,
			ns, content, pgvector.NewVector(vector), confidence, now,
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	insertEpisode := func(ns int64, content string, vector []float32) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO episodes (namespace_id, content, embedding, embedding_model, occurred_at)
			 VALUES ($1, $2, $3, 'test-embedding', $4) RETURNING id`,
			ns, content, pgvector.NewVector(vector), now,
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	// Score 0.5 fact vs score 0.9 episode, limit 1: the episode must win.
	factID := insertFact(nsID, "older consolidated fact", []float32{0.5, 0.8660254, 0}, 0.9)
	episodeID := insertEpisode(nsID, "newer unconsolidated episode", []float32{0.9, 0.43588989, 0})
	// A perfect match in ANOTHER namespace must never leak in.
	_ = insertEpisode(otherID, "other namespace episode", []float32{1, 0, 0})

	got, err := br.Recall(ctx, []string{slug}, "what changed recently", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != episodeID || got[0].Type != "episode" {
		t.Fatalf("limit=1 recall = %+v, want the 0.9 episode %d (fact %d scores 0.5)", got, episodeID, factID)
	}

	all, err := br.Recall(ctx, []string{slug}, "what changed recently", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.Content == "other namespace episode" {
			t.Fatal("episode from another namespace leaked into recall")
		}
	}
	if len(all) != 2 {
		t.Fatalf("limit=10 recall returned %d results, want the fact and the episode: %+v", len(all), all)
	}

	// Equal scores: the existing tie-break (confidence, then fact before episode) keeps the
	// curated fact first. This PR must not change that policy.
	tieSlug := fmt.Sprintf("/recall-default-tie-%d", stamp)
	tieID, err := br.CreateNamespace(ctx, tieSlug, "Recall default path tie", "integration test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(ctx, "DELETE FROM namespaces WHERE id = $1", tieID) }()
	tieFact := insertFact(tieID, "tie fact", []float32{0.6, 0.8, 0}, 0.9)
	_ = insertEpisode(tieID, "tie episode", []float32{0.6, 0.8, 0})
	tie, err := br.Recall(ctx, []string{tieSlug}, "tie", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tie) != 1 || tie[0].ID != tieFact {
		t.Fatalf("equal-score tie = %+v, want the fact %d first", tie, tieFact)
	}
}
