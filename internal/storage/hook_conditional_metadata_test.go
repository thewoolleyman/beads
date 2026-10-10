package storage

import (
	"context"
	"errors"
	"github.com/steveyegge/beads/internal/types"
	"testing"
)

type conditionalHookStore struct {
	fakeHookStore
	replayed bool
	err      error
}

func (s conditionalHookStore) UpdateMetadataConditionally(context.Context, string, ConditionalMetadataRequest, string) (*ConditionalMetadataResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &ConditionalMetadataResult{Replayed: s.replayed}, nil
}

func TestConditionalMetadataHookOnlyOnNewCommit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replayed bool
		err      error
		want     int
	}{
		{name: "new commit", want: 1}, {name: "receipt replay", replayed: true}, {name: "failed commit", err: errors.New("commit indeterminate")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := conditionalHookStore{fakeHookStore: fakeHookStore{issues: map[string]*types.Issue{"test-one": {ID: "test-one"}}}, replayed: tc.replayed, err: tc.err}
			runner := &recordingHookRunner{}
			h := &HookFiringStore{DoltStorage: inner, inner: inner, runner: runner}
			_, err := h.UpdateMetadataConditionally(context.Background(), "test-one", ConditionalMetadataRequest{}, "tester")
			if !errors.Is(err, tc.err) {
				t.Fatal(err)
			}
			if len(runner.events) != tc.want {
				t.Fatal("incorrect hook count", runner.events)
			}
		})
	}
}
