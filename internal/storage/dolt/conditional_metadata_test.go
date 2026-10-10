package dolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

func seedConditionalIssue(t *testing.T, s *DoltStore, id string) {
	t.Helper()
	err := s.CreateIssue(context.Background(), &types.Issue{ID: id, Title: "Conditional metadata", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 2, Metadata: json.RawMessage(`{"foreign":{"n":42}}`)}, "tester")
	if err != nil {
		t.Fatal(err)
	}
}

func conditionalRequest(op string) storage.ConditionalMetadataRequest {
	return storage.ConditionalMetadataRequest{OperationID: op, ExpectedMetadata: json.RawMessage(`{"foreign":{"n":42}}`), Metadata: json.RawMessage(`{"foreign":{"n":42},"result":"new"}`), Comments: []string{"handoff"}}
}

func TestConditionalMetadataReplayAndStale(t *testing.T) {
	s, done := setupConcurrentTestStore(t)
	defer done()
	ctx := context.Background()
	id := "test-conditional"
	seedConditionalIssue(t, s, id)
	r := conditionalRequest("one")
	first, err := s.UpdateMetadataConditionally(ctx, id, r, "tester")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.UpdateMetadataConditionally(ctx, id, r, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed {
		t.Fatal("retry did not identify replay")
	}
	replay.Replayed = false
	if !reflect.DeepEqual(first, replay) {
		t.Fatal("retry changed original result")
	}
	comments, err := s.GetIssueComments(ctx, id)
	if err != nil || len(comments) != 1 {
		t.Fatal(comments, err)
	}
	if !comments[0].CreatedAt.Equal(first.Comments[0].CreatedAt) {
		t.Fatal("receipt/native timestamp differs")
	}
	stale := r
	stale.OperationID = "stale"
	if _, err = s.UpdateMetadataConditionally(ctx, id, stale, "tester"); !errors.Is(err, storage.ErrConditionalStale) {
		t.Fatal("expected stale refusal", err)
	}
	changed := r
	changed.Comments = []string{"different"}
	if _, err = s.UpdateMetadataConditionally(ctx, id, changed, "tester"); !errors.Is(err, storage.ErrConditionalIdentity) {
		t.Fatal("expected operation reuse refusal", err)
	}
	if _, err = s.UpdateMetadataConditionally(ctx, id, r, "different actor"); !errors.Is(err, storage.ErrConditionalIdentity) {
		t.Fatal("expected actor reuse refusal", err)
	}
	next := r
	next.OperationID = "two"
	next.ExpectedMetadata = first.Metadata
	next.Metadata = first.Metadata
	second, err := s.UpdateMetadataConditionally(ctx, id, next, "tester")
	if err != nil {
		t.Fatal(err)
	}
	replay, err = s.UpdateMetadataConditionally(ctx, id, r, "tester")
	if err != nil || replay.Revision != first.Revision {
		t.Fatal("old retry not stable", err)
	}
	current, err := s.GetIssue(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := storage.StrictJSONObject(current.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	want, err := storage.StrictJSONObject(second.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("old replay restored stale metadata")
	}
}

func TestConditionalMetadataOverlappingTransactions(t *testing.T) {
	s, done := setupConcurrentTestStore(t)
	defer done()
	ctx := context.Background()
	for _, same := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			id := fmt.Sprintf("test-race-%t-%t", same, reverse)
			seedConditionalIssue(t, s, id)
			txs := make([]*sql.Tx, 2)
			results := make([]*storage.ConditionalMetadataResult, 2)
			requests := []storage.ConditionalMetadataRequest{conditionalRequest("one"), conditionalRequest("two")}
			if same {
				requests[1] = requests[0]
			}
			for i := range txs {
				var err error
				txs[i], err = s.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer txs[i].Rollback()
			}
			// Both actual transactions read the identical prior BEFORE either writes.
			for _, tx := range txs {
				var meta string
				if err := tx.QueryRowContext(ctx, "SELECT metadata FROM issues WHERE id=?", id).Scan(&meta); err != nil {
					t.Fatal(err)
				}
			}
			for i, tx := range txs {
				hash, rid, err := conditionalMetadataIdentity(id, requests[i], "tester")
				if err != nil {
					t.Fatal(err)
				}
				results[i], err = applyConditionalMetadataInTx(ctx, tx, id, requests[i], "tester", hash, rid)
				if err != nil {
					t.Fatal(err)
				}
			}
			winner, loser := 0, 1
			if reverse {
				winner, loser = 1, 0
			}
			if err := txs[winner].Commit(); err != nil {
				t.Fatal(err)
			}
			if err := txs[loser].Commit(); err == nil {
				t.Fatal("both conflicting transactions committed")
			}
			comments, err := s.GetIssueComments(ctx, id)
			if err != nil || len(comments) != 1 {
				t.Fatal("loser comment survived", comments, err)
			}
			if comments[0].ID != results[winner].Comments[0].ID {
				t.Fatal("wrong comment survived")
			}
			retry, err := s.UpdateMetadataConditionally(ctx, id, requests[loser], "tester")
			if same {
				if err != nil || !retry.Replayed || retry.Revision != results[winner].Revision {
					t.Fatal("concurrent identical retry failed", err)
				}
			} else if !errors.Is(err, storage.ErrConditionalStale) {
				t.Fatal("loser retry not stale", err)
			}
		}
	}
}

func TestConditionalMetadataRollback(t *testing.T) {
	s, done := setupConcurrentTestStore(t)
	defer done()
	ctx := context.Background()
	id := "test-rollback"
	seedConditionalIssue(t, s, id)
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE comments ADD CONSTRAINT reject_comment CHECK (`text` <> 'reject')"); err != nil {
		t.Fatal(err)
	}
	r := conditionalRequest("rollback")
	r.Comments = []string{"first", "reject"}
	if _, err := s.UpdateMetadataConditionally(ctx, id, r, "tester"); err == nil {
		t.Fatal("expected insertion failure")
	}
	comments, err := s.GetIssueComments(ctx, id)
	if err != nil || len(comments) != 0 {
		t.Fatal("partial comments survived", comments, err)
	}
	// The original prior is still usable; failed operation left no receipt.
	r.Comments = []string{"accepted"}
	if _, err := s.UpdateMetadataConditionally(ctx, id, r, "tester"); err != nil {
		t.Fatal("rollback damaged prior or left receipt", err)
	}
}

func TestConditionalMetadataRefusesLossyJSON(t *testing.T) {
	s, done := setupConcurrentTestStore(t)
	defer done()
	ctx := context.Background()
	id := "test-lossy"
	seedConditionalIssue(t, s, id)
	r := conditionalRequest("lossy")
	r.Metadata = json.RawMessage(`{"foreign":{"n":9007199254740993}}`)
	if _, err := s.UpdateMetadataConditionally(ctx, id, r, "tester"); err == nil {
		t.Fatal("backend accepted a rounded integer")
	}
	comments, err := s.GetIssueComments(ctx, id)
	if err != nil || len(comments) != 0 {
		t.Fatal("lossy refusal changed comments", comments, err)
	}
	r.Metadata = conditionalRequest("lossy").Metadata
	if _, err := s.UpdateMetadataConditionally(ctx, id, r, "tester"); err != nil {
		t.Fatal("lossy refusal changed prior authority", err)
	}
}

func TestConditionalMetadataReceiptFailureRollback(t *testing.T) {
	s, done := setupConcurrentTestStore(t)
	defer done()
	ctx := context.Background()
	id := "test-receipt-rollback"
	seedConditionalIssue(t, s, id)
	r := conditionalRequest("receipt-failure")
	_, rid, err := conditionalMetadataIdentity(id, r, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, "ALTER TABLE events ADD CONSTRAINT reject_receipt CHECK (id <> '"+rid+"')"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMetadataConditionally(ctx, id, r, "tester"); err == nil {
		t.Fatal("expected receipt failure")
	}
	comments, err := s.GetIssueComments(ctx, id)
	if err != nil || len(comments) != 0 {
		t.Fatal("partial comments survived", comments, err)
	}
	if _, err = s.db.ExecContext(ctx, "ALTER TABLE events DROP CHECK reject_receipt"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMetadataConditionally(ctx, id, r, "tester"); err != nil {
		t.Fatal("failed receipt changed prior", err)
	}
}

func TestConditionalMetadataUnsafeModeAndUnrelatedIssues(t *testing.T) {
	s, done := setupConcurrentTestStore(t)
	defer done()
	ctx := context.Background()
	for _, id := range []string{"test-safe-a", "test-safe-b"} {
		seedConditionalIssue(t, s, id)
	}
	for _, setting := range []string{"dolt_allow_commit_conflicts", "dolt_force_transaction_commit"} {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, "SET @@"+setting+" = 1"); err != nil {
			t.Fatal(err)
		}
		r := conditionalRequest(setting)
		hash, rid, _ := conditionalMetadataIdentity("test-safe-a", r, "tester")
		_, refusal := applyConditionalMetadataInTx(ctx, tx, "test-safe-a", r, "tester", hash, rid)
		if _, err = tx.ExecContext(ctx, "SET @@"+setting+" = 0"); err != nil {
			t.Fatal(err)
		}
		_ = tx.Rollback()
		if refusal == nil {
			t.Fatal("unsafe session admitted")
		}
	}
	txs := make([]*sql.Tx, 2)
	for i, id := range []string{"test-safe-a", "test-safe-b"} {
		var err error
		txs[i], err = s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer txs[i].Rollback()
		r := conditionalRequest("independent")
		hash, rid, _ := conditionalMetadataIdentity(id, r, "tester")
		if _, err = applyConditionalMetadataInTx(ctx, txs[i], id, r, "tester", hash, rid); err != nil {
			t.Fatal(err)
		}
	}
	for _, tx := range txs {
		if err := tx.Commit(); err != nil {
			t.Fatal("unrelated issue conflicted", err)
		}
	}
}

func TestConditionalMetadataUnsupportedRows(t *testing.T) {
	s, done := setupConcurrentTestStore(t)
	defer done()
	ctx := context.Background()
	for _, kind := range []string{"template", "wisp", "ephemeral", "missing"} {
		id := "test-unsupported-" + kind
		if kind != "missing" {
			seedConditionalIssue(t, s, id)
		}
		var err error
		switch kind {
		case "template":
			_, err = s.db.ExecContext(ctx, "UPDATE issues SET is_template=1 WHERE id=?", id)
		case "ephemeral":
			_, err = s.db.ExecContext(ctx, "UPDATE issues SET ephemeral=1 WHERE id=?", id)
		case "wisp":
			_, err = s.db.ExecContext(ctx, "INSERT INTO wisps (id,title,status,issue_type,priority) VALUES (?, 'collision','open','task',2)", id)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.UpdateMetadataConditionally(ctx, id, conditionalRequest(kind), "tester"); err == nil {
			t.Fatal("unsupported row admitted", kind)
		}
		for _, table := range []string{"comments", "wisp_comments"} {
			var count int
			if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE issue_id=?", id).Scan(&count); err != nil || count != 0 {
				t.Fatal("refusal appended comments", kind, table, count, err)
			}
		}
	}
}

func TestConditionalMetadataLargeReceiptAndCorruption(t *testing.T) {
	s, done := setupConcurrentTestStore(t)
	defer done()
	ctx := context.Background()
	id := "test-large-receipt"
	seedConditionalIssue(t, s, id)
	r := conditionalRequest("large")
	r.Comments = make([]string, 32)
	for i := range r.Comments {
		r.Comments[i] = strings.Repeat("c", 800)
	}
	actor := strings.Repeat("a", 255)
	first, err := s.UpdateMetadataConditionally(ctx, id, r, actor)
	if err != nil {
		t.Fatal(err)
	}
	_, rid, _ := conditionalMetadataIdentity(id, r, actor)
	var raw string
	if err = s.db.QueryRowContext(ctx, "SELECT comment FROM events WHERE id=?", rid).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 32768 {
		t.Fatal("fixture did not exceed request bound", len(raw))
	}
	replay, err := s.UpdateMetadataConditionally(ctx, id, r, actor)
	if err != nil || !replay.Replayed || replay.Revision != first.Revision {
		t.Fatal("valid large receipt not replayed", err)
	}
	for _, bad := range []string{raw + " {}", strings.Replace(raw, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(raw, `"replayed":false`, `"replayed":true`, 1)} {
		if _, err = s.db.ExecContext(ctx, "UPDATE events SET comment=? WHERE id=?", bad, rid); err != nil {
			t.Fatal(err)
		}
		if _, err = s.UpdateMetadataConditionally(ctx, id, r, actor); !errors.Is(err, storage.ErrConditionalReceipt) {
			t.Fatal("corrupt receipt accepted", err)
		}
	}
	comments, err := s.GetIssueComments(ctx, id)
	if err != nil || len(comments) != 32 {
		t.Fatal("receipt refusal appended", len(comments), err)
	}
}
