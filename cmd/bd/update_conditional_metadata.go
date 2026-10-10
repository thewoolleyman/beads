package main

import (
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/steveyegge/beads/internal/storage"
)

var conditionalMetadataID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// runConditionalMetadataUpdate is deliberately a separate path from update's
// best-effort multi-ID loop: a refusal must produce a nonzero exit, not print
// an error and continue. No routing, abbreviated IDs or last-touched fallback.
func runConditionalMetadataUpdate(cmd *cobra.Command, args []string) error {
	if len(args) != 1 || !conditionalMetadataID.MatchString(args[0]) {
		return HandleErrorRespectJSON("conditional metadata update requires one exact local issue ID")
	}
	conflict := ""
	cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Changed && f.Name != "conditional-metadata-file" {
			conflict = f.Name
		}
	})
	if conflict != "" {
		return HandleErrorRespectJSON("conditional metadata update cannot be combined with --%s", conflict)
	}
	if usesProxiedServer() {
		return HandleErrorRespectJSON("%v: proxied server", storage.ErrConditionalUnsupported)
	}
	backend, ok := getStore().(storage.ConditionalMetadataStore)
	if !ok {
		return HandleErrorRespectJSON("%v", storage.ErrConditionalUnsupported)
	}
	path, _ := cmd.Flags().GetString("conditional-metadata-file")
	var input io.Reader = cmd.InOrStdin()
	if path != "-" {
		// The local operator explicitly selects this input file; there is no
		// remote path input or constrained directory to escape.
		f, err := os.Open(path) // #nosec G304 -- intentional CLI input file
		if err != nil {
			return HandleErrorRespectJSON("conditional metadata request: %v", err)
		}
		defer f.Close()
		input = f
	}
	raw, err := io.ReadAll(io.LimitReader(input, storage.ConditionalMetadataMaxBytes+1))
	if err != nil {
		return HandleErrorRespectJSON("conditional metadata request: %v", err)
	}
	request, err := storage.DecodeConditionalMetadataRequest(raw)
	if err != nil {
		return HandleErrorRespectJSON("conditional metadata request: %v", err)
	}
	result, err := backend.UpdateMetadataConditionally(rootCtx, args[0], request, getActor())
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	if !result.Replayed {
		commandDidWrite.Store(true)
	}
	if jsonOutput {
		return outputJSON(result)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Conditional metadata operation %s on %s: revision %s (replayed=%t)\n", result.OperationID, result.IssueID, result.Revision, result.Replayed)
	}
	return nil
}
