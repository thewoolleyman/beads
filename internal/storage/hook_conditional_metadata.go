package storage

import (
	"context"
	"github.com/steveyegge/beads/internal/hooks"
)

// UpdateMetadataConditionally forwards the optional capability without bypassing
// the ordinary hook boundary. Hooks are best effort after a NEW commit only;
// replay does not attempt to recover an external hook missed after a crash.
func (h *HookFiringStore) UpdateMetadataConditionally(ctx context.Context, id string, r ConditionalMetadataRequest, actor string) (*ConditionalMetadataResult, error) {
	backend, ok := h.inner.(ConditionalMetadataStore)
	if !ok {
		return nil, ErrConditionalUnsupported
	}
	result, err := backend.UpdateMetadataConditionally(ctx, id, r, actor)
	if err == nil && !result.Replayed {
		h.fireHookByID(ctx, hooks.EventUpdate, id)
	}
	return result, err
}
