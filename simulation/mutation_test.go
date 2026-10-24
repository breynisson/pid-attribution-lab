package simulation_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Mutate the real implementation through temporary Go build overlays. No fault
// switches enter production code, and the repository source is never rewritten.
func TestTargetedMutations(t *testing.T) {
	if os.Getenv("PID_ATTRIBUTION_FAULTY") == "1" {
		t.Skip("mutation checks require the corrected implementation as their baseline")
	}
	sourcePath, err := filepath.Abs("attributor.go")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		test   string
		mutate func(*testing.T, string) string
	}{
		{"ignore-incarnation", "TestNewEventWithOnlyPreviousLifetimeMetadata", func(t *testing.T, s string) string {
			return eraseIdentityField(t, s, "key.Incarnation = 0")
		}},
		{"ignore-scope", "TestScopeIsolation", func(t *testing.T, s string) string {
			return eraseIdentityField(t, s, `key.Scope = ""`)
		}},
		{"delete-history-on-exit", "TestDelayedEventAfterPIDReuse", func(t *testing.T, s string) string {
			return mutationReplace(t, s, "func (a *Attributor) DeliverExit(_ ProcessKey) {}",
				"func (a *Attributor) DeliverExit(key ProcessKey) { delete(a.metadata, key) }", 1)
		}},
		{"always-unresolved", "TestActivityBeforeMetadata", func(t *testing.T, s string) string {
			// Disable both immediate resolution and metadata-triggered resolution.
			s = mutationReplace(t, s, "if metadata, ok := a.metadata[event.Key]; ok {",
				"if metadata, ok := a.metadata[event.Key]; ok && false {", 1)
			return mutationReplace(t, s,
				"EventID: eventID, Resolved: true, Key: metadata.Key, Workload: metadata.Workload,",
				"EventID: eventID,", 1)
		}},
		{"remove-event-deduplication", "TestEventIDKeepsFirstOrigin", func(t *testing.T, s string) string {
			return mutationReplace(t, s,
				"\tif result, exists := a.results[event.EventID]; exists {\n\t\treturn result\n\t}\n", "", 1)
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			// Verify the named contract passes unmodified, even when this
			// mutation check is invoked on its own with -run.
			if passed, output := runNamedContract(t, mutation.test, ""); !passed {
				t.Fatalf("unmodified baseline failed:\n%s", output)
			}
			dir := t.TempDir()
			mutantPath := filepath.Join(dir, "attributor.go")
			if err := os.WriteFile(mutantPath, []byte(mutation.mutate(t, string(source))), 0600); err != nil {
				t.Fatal(err)
			}
			overlay, err := json.Marshal(map[string]map[string]string{"Replace": {sourcePath: mutantPath}})
			if err != nil {
				t.Fatal(err)
			}
			overlayPath := filepath.Join(dir, "overlay.json")
			if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
				t.Fatal(err)
			}
			passed, output := runNamedContract(t, mutation.test, overlayPath)
			if passed {
				t.Fatalf("mutation survived %s", mutation.test)
			}
			t.Logf("caught by %s:\n%s", mutation.test, output)
		})
	}
}

func mutationReplace(t *testing.T, source, old, replacement string, count int) string {
	t.Helper()
	if got := strings.Count(source, old); got != count {
		t.Fatalf("mutation anchor changed: expected %d occurrences of %q, found %d", count, old, got)
	}
	return strings.ReplaceAll(source, old, replacement)
}

// Change only storage/lookup keys, preserving the full key in metadata payloads
// and results. Thus failures expose cross-matches, not merely altered output keys.
func eraseIdentityField(t *testing.T, source, assignment string) string {
	t.Helper()
	for _, edit := range []struct {
		old, replacement string
		count            int
	}{
		{"a.metadata[metadata.Key]", "a.metadata[mutationKey(metadata.Key)]", 1},
		{"a.pending[metadata.Key]", "a.pending[mutationKey(metadata.Key)]", 1},
		{"delete(a.pending, metadata.Key)", "delete(a.pending, mutationKey(metadata.Key))", 1},
		{"a.metadata[event.Key]", "a.metadata[mutationKey(event.Key)]", 1},
		{"a.pending[event.Key]", "a.pending[mutationKey(event.Key)]", 2},
	} {
		source = mutationReplace(t, source, edit.old, edit.replacement, edit.count)
	}
	return source + "\nfunc mutationKey(key ProcessKey) ProcessKey { " + assignment + "; return key }\n"
}

func runNamedContract(t *testing.T, name, overlay string) (bool, string) {
	t.Helper()
	args := []string{"test", "-json", ".", "-run", "^" + name + "$", "-count=1", "-timeout=30s"}
	if overlay != "" {
		args = append(args, "-overlay", overlay)
	}
	output, err := exec.Command("go", args...).CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("cannot run contract test: %v\n%s", err, output)
	}
	var action string
	var details strings.Builder
	for _, line := range bytes.Split(output, []byte("\n")) {
		var event struct{ Action, Test, Output string }
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if event.Test == name && (event.Action == "pass" || event.Action == "fail") {
			action = event.Action
		}
		if event.Test == name || strings.HasPrefix(event.Test, name+"/") {
			details.WriteString(event.Output)
		}
	}
	// A build failure, missing test, or process error is not a caught mutation.
	if action == "" || (action == "pass" && err != nil) || (action == "fail" && err == nil) {
		t.Fatalf("contract test did not finish normally: %v\n%s", err, output)
	}
	return action == "pass", details.String()
}
