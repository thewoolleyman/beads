package dolt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

type conditionalMetadataReceipt struct {
	Version     int                               `json:"version"`
	RequestHash string                            `json:"request_hash"`
	Result      storage.ConditionalMetadataResult `json:"result"`
}

// UpdateMetadataConditionally performs one SQL transaction on regular issues.
// No DOLT_COMMIT is issued here. Concurrent writers are resolved by the native
// transaction boundary, not a plugin or backend advisory lock. A fresh random
// revision makes even concurrent identical operations conflict at commit.
func (s *DoltStore) UpdateMetadataConditionally(ctx context.Context, id string, request storage.ConditionalMetadataRequest, actor string) (*storage.ConditionalMetadataResult, error) {
	if s.closed.Load() {
		return nil, ErrStoreClosed
	}
	if s.readOnly {
		return nil, fmt.Errorf("conditional metadata update: read-only store")
	}
	if err := storage.ValidateConditionalMetadataRequest(request); err != nil {
		return nil, err
	}
	if actor == "" || len(actor) > 255 || !utf8.ValidString(actor) {
		return nil, fmt.Errorf("actor must contain 1..255 UTF-8 bytes")
	}
	digest, receiptID, err := conditionalMetadataIdentity(id, request, actor)
	if err != nil {
		return nil, err
	}
	var result *storage.ConditionalMetadataResult
	err = s.withWriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = applyConditionalMetadataInTx(ctx, tx, id, request, actor, digest, receiptID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("conditional metadata operation %q: %w", request.OperationID, err)
	}
	return result, nil
}

func applyConditionalMetadataInTx(ctx context.Context, tx *sql.Tx, id string, request storage.ConditionalMetadataRequest, actor, digest, receiptID string) (*storage.ConditionalMetadataResult, error) {
	var conflictMode, forceMode int
	if err := tx.QueryRowContext(ctx, "SELECT @@dolt_allow_commit_conflicts, @@dolt_force_transaction_commit").Scan(&conflictMode, &forceMode); err != nil {
		return nil, err
	}
	if conflictMode != 0 || forceMode != 0 {
		return nil, fmt.Errorf("conditional metadata update requires conflict commits disabled")
	}
	// Only regular, retained issues are supported; never mutate a demoted wisp.
	var current sql.NullString
	var ephemeral, template bool
	if err := tx.QueryRowContext(ctx, "SELECT metadata, ephemeral, is_template FROM issues WHERE id = ?", id).Scan(&current, &ephemeral, &template); err != nil {
		return nil, fmt.Errorf("conditional metadata issue: %w", err)
	}
	if ephemeral || template {
		return nil, fmt.Errorf("%w: ephemeral or read-only template issue", storage.ErrConditionalUnsupported)
	}
	var wispCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM wisps WHERE id = ?", id).Scan(&wispCount); err != nil {
		return nil, err
	}
	if wispCount != 0 {
		return nil, fmt.Errorf("%w: issue identity also exists as a wisp", storage.ErrConditionalUnsupported)
	}
	replay, err := readConditionalMetadataReceipt(ctx, tx, receiptID, id, request.OperationID, digest)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		return replay, nil
	}
	raw := []byte(current.String)
	if !current.Valid || len(raw) == 0 {
		raw = []byte("{}")
	}
	actual, err := storage.StrictJSONObject(raw)
	if err != nil {
		return nil, fmt.Errorf("existing metadata: %w", err)
	}
	expected, _ := storage.StrictJSONObject(request.ExpectedMetadata)
	ac, _ := json.Marshal(actual)
	ex, _ := json.Marshal(expected)
	if !bytes.Equal(ac, ex) {
		return nil, fmt.Errorf("%w: %s", storage.ErrConditionalStale, id)
	}
	desired, _ := storage.StrictJSONObject(request.Metadata)
	revision := uuid.NewString()
	desired[storage.ConditionalMetadataRevisionKey] = revision
	metadata, err := json.Marshal(desired)
	if err != nil {
		return nil, err
	}
	if err := issueops.ValidateMetadataIfConfigured(metadata); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, "UPDATE issues SET metadata = CAST(? AS JSON), updated_at = ? WHERE id = ? AND metadata <=> CAST(? AS JSON)", string(metadata), time.Now().UTC(), id, nullableMetadata(current))
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, fmt.Errorf("%w: %s", storage.ErrConditionalStale, id)
	}
	// Refuse any backend JSON normalization that would change the requested
	// value or its defined numeric spelling; never return an unpersisted value.
	var persisted string
	if err := tx.QueryRowContext(ctx, "SELECT metadata FROM issues WHERE id = ?", id).Scan(&persisted); err != nil {
		return nil, err
	}
	persistedObject, err := storage.StrictJSONObject([]byte(persisted))
	if err != nil {
		return nil, err
	}
	persistedCanonical, err := json.Marshal(persistedObject)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(metadata, persistedCanonical) {
		return nil, fmt.Errorf("conditional metadata is not exactly representable by this backend")
	}
	result := &storage.ConditionalMetadataResult{OperationID: request.OperationID, IssueID: id, Revision: revision, Metadata: metadata, Comments: []*types.Comment{}}
	for _, text := range request.Comments {
		// SQL stores second precision. Freeze the same precision in the receipt.
		comment, err := issueops.ImportIssueCommentInTx(ctx, tx, id, actor, text, time.Now().UTC().Truncate(time.Second))
		if err != nil {
			return nil, err
		}
		result.Comments = append(result.Comments, comment)
	}
	receipt, err := json.Marshal(conditionalMetadataReceipt{Version: 1, RequestHash: digest, Result: *result})
	if err != nil {
		return nil, err
	}
	if len(receipt) > 60000 {
		return nil, fmt.Errorf("conditional metadata receipt exceeds 60000 bytes")
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)", receiptID, id, types.EventUpdated, actor, string(receipt))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func nullableMetadata(raw sql.NullString) any {
	if !raw.Valid {
		return nil
	}
	return raw.String
}

func conditionalMetadataIdentity(id string, r storage.ConditionalMetadataRequest, actor string) (string, string, error) {
	expected, err := storage.StrictJSONObject(r.ExpectedMetadata)
	if err != nil {
		return "", "", err
	}
	desired, err := storage.StrictJSONObject(r.Metadata)
	if err != nil {
		return "", "", err
	}
	comments := r.Comments
	if comments == nil {
		comments = []string{}
	}
	payload, err := json.Marshal([]any{"beads-conditional-metadata-v1", id, r.OperationID, actor, expected, desired, comments})
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256(payload)
	identity, _ := json.Marshal([]string{"beads-conditional-metadata-v1", id, r.OperationID})
	return hex.EncodeToString(hash[:]), uuid.NewSHA1(uuid.NameSpaceOID, identity).String(), nil
}

func readConditionalMetadataReceipt(ctx context.Context, tx *sql.Tx, eventID, id, operation, digest string) (*storage.ConditionalMetadataResult, error) {
	var issue, eventType string
	var raw sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT issue_id, event_type, comment FROM events WHERE id = ?", eventID).Scan(&issue, &eventType, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if issue != id || eventType != string(types.EventUpdated) || !raw.Valid {
		return nil, storage.ErrConditionalReceipt
	}
	if len(raw.String) > 60000 {
		return nil, storage.ErrConditionalReceipt
	}
	var receipt conditionalMetadataReceipt
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw.String)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil || receipt.Version != 1 || receipt.Result.IssueID != id || receipt.Result.OperationID != operation || receipt.Result.Revision == "" || receipt.Result.Replayed {
		return nil, storage.ErrConditionalReceipt
	}
	// Receipts are our versioned canonical encoding, never user-authored JSON.
	// Re-encoding rejects duplicate/unknown members and trailing data, including
	// fields otherwise tolerated by Comment's legacy custom decoder.
	encoded, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(encoded, []byte(raw.String)) {
		return nil, storage.ErrConditionalReceipt
	}
	metadata, err := storage.StrictJSONObject(receipt.Result.Metadata)
	if err != nil || metadata[storage.ConditionalMetadataRevisionKey] != receipt.Result.Revision || receipt.Result.Comments == nil {
		return nil, storage.ErrConditionalReceipt
	}
	for _, comment := range receipt.Result.Comments {
		if comment == nil || comment.ID == "" || comment.IssueID != id || comment.CreatedAt.IsZero() {
			return nil, storage.ErrConditionalReceipt
		}
	}
	if receipt.RequestHash != digest {
		return nil, storage.ErrConditionalIdentity
	}
	receipt.Result.Replayed = true
	return &receipt.Result, nil
}
