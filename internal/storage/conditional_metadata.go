package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/steveyegge/beads/internal/types"
)

// ConditionalMetadataStore is an optional backend capability. It changes one
// regular issue's complete metadata and appends comments in one transaction.
// It does not modify lifecycle fields, labels, dependencies or other issues.
type ConditionalMetadataStore interface {
	UpdateMetadataConditionally(context.Context, string, ConditionalMetadataRequest, string) (*ConditionalMetadataResult, error)
}

var (
	ErrConditionalUnsupported = errors.New("conditional metadata update unsupported")
	ErrConditionalStale       = errors.New("conditional metadata prior state mismatch")
	ErrConditionalIdentity    = errors.New("conditional metadata operation identity mismatch")
	ErrConditionalReceipt     = errors.New("conditional metadata receipt invalid")
)

// ConditionalMetadataRequest explicitly replaces the complete metadata object.
// OperationID and actor must be retained across retries, including a lost reply.
// ExpectedMetadata must be copied from the original observation, not refreshed
// when retrying the same operation. Comments preserve their requested order.
type ConditionalMetadataRequest struct {
	OperationID      string          `json:"operation_id"`
	ExpectedMetadata json.RawMessage `json:"expected_metadata"`
	Metadata         json.RawMessage `json:"metadata"`
	Comments         []string        `json:"comments"`
}

// ConditionalMetadataResult is frozen in the receipt before commit. Replay
// returns this original result even after later successful transitions, without
// restoring its metadata or firing another update hook.
type ConditionalMetadataResult struct {
	OperationID string           `json:"operation_id"`
	IssueID     string           `json:"issue_id"`
	Revision    string           `json:"revision"`
	Metadata    json.RawMessage  `json:"metadata"`
	Comments    []*types.Comment `json:"comments"`
	Replayed    bool             `json:"replayed"`
}
