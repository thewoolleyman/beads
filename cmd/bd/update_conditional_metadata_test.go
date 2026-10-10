package main

import (
	"context"
	"encoding/json"
	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/storage"
	"strings"
	"testing"
)

type conditionalCLIStore struct {
	storage.DoltStorage
	calls    int
	replayed bool
}

func (s *conditionalCLIStore) UpdateMetadataConditionally(_ context.Context, id string, r storage.ConditionalMetadataRequest, _ string) (*storage.ConditionalMetadataResult, error) {
	s.calls++
	return &storage.ConditionalMetadataResult{IssueID: id, OperationID: r.OperationID, Revision: "revision", Metadata: r.Metadata, Replayed: s.replayed}, nil
}

func TestConditionalMetadataCLIRouting(t *testing.T) {
	oldStore, oldJSON, oldProxy := store, jsonOutput, proxiedServerMode
	oldCtx := rootCtx
	t.Cleanup(func() { store = oldStore; jsonOutput = oldJSON; proxiedServerMode = oldProxy; rootCtx = oldCtx })
	rootCtx = context.Background()
	jsonOutput = true
	proxiedServerMode = false
	for _, tc := range []struct {
		name     string
		args     []string
		input    string
		extra    bool
		replay   bool
		envelope bool
		large    bool
		ok       bool
	}{
		{name: "missing ID", input: `{}`},
		{name: "multiple IDs", args: []string{"test-a", "test-b"}, input: `{}`},
		{name: "routed ID", args: []string{"other:test-a"}, input: `{}`},
		{name: "mutation flag", args: []string{"test-a"}, extra: true, input: `{"operation_id":"a","expected_metadata":{},"metadata":{},"comments":[]}`},
		{name: "duplicate field", args: []string{"test-a"}, input: `{"operation_id":"a","operation_id":"b"}`},
		{name: "new commit", args: []string{"test-a"}, input: `{"operation_id":"a","expected_metadata":{},"metadata":{},"comments":[]}`, ok: true},
		{name: "large legacy", args: []string{"test-a"}, input: `{"operation_id":"a","expected_metadata":{},"metadata":{"integer":9007199254740993},"comments":[]}`, ok: true, large: true},
		{name: "large envelope", args: []string{"test-a"}, input: `{"operation_id":"a","expected_metadata":{},"metadata":{"integer":9007199254740993},"comments":[]}`, ok: true, envelope: true, large: true},
		{name: "envelope", args: []string{"test-a"}, input: `{"operation_id":"a","expected_metadata":{},"metadata":{},"comments":[]}`, ok: true, envelope: true},
		{name: "replay", args: []string{"test-a"}, input: `{"operation_id":"a","expected_metadata":{},"metadata":{},"comments":[]}`, ok: true, replay: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BD_JSON_ENVELOPE", "0")
			if tc.envelope {
				t.Setenv("BD_JSON_ENVELOPE", "1")
			}
			fake := &conditionalCLIStore{replayed: tc.replay}
			store = fake
			commandDidWrite.Store(false)
			cmd := &cobra.Command{Use: "update"}
			parent := &cobra.Command{Use: "bd"}
			parent.AddCommand(cmd)
			cmd.Flags().String("conditional-metadata-file", "", "")
			cmd.Flags().String("title", "", "")
			if err := cmd.Flags().Set("conditional-metadata-file", "-"); err != nil {
				t.Fatal(err)
			}
			if tc.extra {
				if err := cmd.Flags().Set("title", "unsafe"); err != nil {
					t.Fatal(err)
				}
			}
			cmd.SetIn(strings.NewReader(tc.input))
			var err error
			out := captureStdout(t, func() error { err = runConditionalMetadataUpdate(cmd, tc.args); return nil })
			if (err == nil) != tc.ok {
				t.Fatal("wrong result", err)
			}
			if tc.ok {
				var result map[string]any
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(err)
				}
				if result["schema_version"] != float64(JSONSchemaVersion) {
					t.Fatal("missing schema version", result)
				}
				if tc.envelope {
					result = result["data"].(map[string]any)
				}
				if tc.large && !strings.Contains(out, "9007199254740993") {
					t.Fatal("metadata integer rounded", out)
				}
				if result["operation_id"] != "a" {
					t.Fatal("wrong JSON result", result)
				}
				if fake.calls != 1 || commandDidWrite.Load() == tc.replay {
					t.Fatal("incorrect write bookkeeping")
				}
			} else if fake.calls != 0 || commandDidWrite.Load() {
				t.Fatal("refused command mutated")
			}
		})
	}
}
