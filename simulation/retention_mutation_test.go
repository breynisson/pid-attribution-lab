package simulation_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRetentionMutations(t *testing.T) {
	if os.Getenv("PID_ATTRIBUTION_FAULTY") == "1" {
		t.Skip("mutation checks require the corrected baseline")
	}
	mutations := []struct{ name, file, test, old, replacement string }{
		{"pid-fallback", "attributor.go", "TestRetentionExpiredHistoryNeverUsesCurrentPID",
			"if metadata, ok := a.metadata[event.Key]; ok {",
			`if _, exists := a.metadata[event.Key]; !exists {
				for _, m := range a.metadata {
					if m.Key.Scope == event.Key.Scope && m.Key.PID == event.Key.PID {
						a.metadata[event.Key] = m
						break
					}
				}
			}
			if metadata, ok := a.metadata[event.Key]; ok {`},
		{"recompute-on-restart", "backend.go", "TestRetentionRecoveryPreservesResultsAndExpiry",
			"b.snapshot, b.core = s, coreFromSnapshot(s)",
			`b.snapshot, b.core = s, New()
			for _, m := range s.Metadata { b.core.DeliverMetadata(m.Metadata) }
			for _, e := range s.Events { b.core.DeliverActivity(e.Activity) }`},
		{"revive-expired", "retention.go", "TestRetentionDuplicatesDoNotRefreshAndExpiryIsTerminal",
			"if !e.Result.Resolved && !e.Expired {", "if !e.Result.Resolved {"},
		{"late-expiry-boundary", "retention.go", "TestRetentionBoundary",
			"now-acceptedAt >= ttl", "now-acceptedAt > ttl"},
		{"always-unresolved", "attributor.go", "TestRetentionBoundary",
			"func (a *Attributor) DeliverMetadata(metadata Metadata) {",
			"func (a *Attributor) DeliverMetadata(metadata Metadata) { return;"},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			if passed, output := runNamedContract(t, m.test, ""); !passed {
				t.Fatalf("baseline failed:\n%s", output)
			}
			path, err := filepath.Abs(m.file)
			if err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mutant := mutationReplace(t, string(source), m.old, m.replacement, 1)
			if m.name == "always-unresolved" {
				mutant = mutationReplace(t, mutant, "if metadata, ok := a.metadata[event.Key]; ok {", "if metadata, ok := a.metadata[event.Key]; ok && false {", 1)
			}
			dir := t.TempDir()
			mutantPath := filepath.Join(dir, m.file)
			if err := os.WriteFile(mutantPath, []byte(mutant), 0600); err != nil {
				t.Fatal(err)
			}
			overlay, err := json.Marshal(map[string]map[string]string{"Replace": {path: mutantPath}})
			if err != nil {
				t.Fatal(err)
			}
			overlayPath := filepath.Join(dir, "overlay.json")
			if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
				t.Fatal(err)
			}
			if passed, output := runNamedContract(t, m.test, overlayPath); passed {
				t.Fatalf("mutation survived %s", m.test)
			} else {
				t.Logf("caught by %s:\n%s", m.test, output)
			}
		})
	}
}
