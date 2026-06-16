package zodgen_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/codegen/zodgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runtimeTimeout bounds the node driver run so a hung interpreter can't stall
// the suite.
const runtimeTimeout = 30 * time.Second

// rtCase is one runtime conformance case: a data shape fed to the emitted
// Collection / CollectionField Zod, with the parse outcome we expect.
type rtCase struct {
	Data   any    `json:"data"`
	Name   string `json:"name"`
	Schema string `json:"schema"` // "field" | "collection"
	Valid  bool   `json:"valid"`
}

// TestCollectionFieldRuntimeConformance executes the ACTUAL emitted Zod
// (CollectionField + Collection) against the lightwave-core#167 cases, proving
// the .superRefine() the emitter writes accepts/rejects the right shapes at
// runtime — the golden/string tests only prove the text was emitted.
//
// The zodgen gate is Go-only (mise.toml pins no JS toolchain), so this lane
// runs wherever node + zod are resolvable and self-skips otherwise. To run it:
//
//	LW_ZOD_NODE_MODULES=~/dev/joelschaeffer-site/node_modules \
//	  go test ./internal/codegen/zodgen -run RuntimeConformance
//
// LW_ZOD_NODE_MODULES must be a node_modules dir containing a (dependency-free)
// zod install; it is symlinked into a temp dir so `import { z } from "zod"`
// resolves.
func TestCollectionFieldRuntimeConformance(t *testing.T) {
	t.Parallel()

	zodNM := os.Getenv("LW_ZOD_NODE_MODULES")
	if zodNM == "" {
		t.Skip("set LW_ZOD_NODE_MODULES to a node_modules dir with zod to run the runtime conformance lane")
	}

	if _, err := os.Stat(filepath.Join(zodNM, "zod")); err != nil {
		t.Skipf("LW_ZOD_NODE_MODULES=%q has no zod/: %v", zodNM, err)
	}

	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping runtime conformance lane")
	}

	component, section, enums := loadFixtures(t)

	extra := make([]*zodgen.Schema, 0, 5)
	for _, name := range []string{"page_definition.yaml", "site_config.yaml", "app_shell.yaml", "collection.yaml", "ui_node.yaml"} {
		s, e := zodgen.LoadSchema(filepath.Join("testdata", "ui", name))
		require.NoError(t, e, name)
		extra = append(extra, s)
	}

	contractsTS, err := zodgen.EmitContracts(append([]*zodgen.Schema{component, section}, extra...), enums)
	require.NoError(t, err)

	// The happy-path case (#5) IS the stamp's example block — decode it so the
	// test can never drift from the schema it guards.
	collection, err := zodgen.LoadSchema(filepath.Join("testdata", "ui", "collection.yaml"))
	require.NoError(t, err)

	var example map[string]any
	require.NoError(t, collection.Example.Decode(&example), "decoding collection example")

	cases := []rtCase{
		// lightwave-core#167 — the five required assertions.
		{Name: "array field with BOTH of_type and of_schema", Schema: "field", Valid: false,
			Data: map[string]any{"name": "x", "type": "array", "of_type": "media", "of_schema": "Contributor"}},
		{Name: "array field with NEITHER of_type nor of_schema", Schema: "field", Valid: false,
			Data: map[string]any{"name": "x", "type": "array"}},
		{Name: "non-array field with of_type set", Schema: "field", Valid: false,
			Data: map[string]any{"name": "x", "type": "text", "of_type": "media"}},
		{Name: "select field with no options", Schema: "field", Valid: false,
			Data: map[string]any{"name": "x", "type": "select"}},
		{Name: "happy-path example", Schema: "collection", Valid: true, Data: example},
		// Companion guards: the rules must not over-reject valid shapes.
		{Name: "non-array field with of_schema set", Schema: "field", Valid: false,
			Data: map[string]any{"name": "x", "type": "text", "of_schema": "Contributor"}},
		{Name: "select field with empty options", Schema: "field", Valid: false,
			Data: map[string]any{"name": "x", "type": "select", "options": []string{}}},
		{Name: "array field with exactly of_type", Schema: "field", Valid: true,
			Data: map[string]any{"name": "x", "type": "array", "of_type": "media"}},
		{Name: "array field with exactly of_schema", Schema: "field", Valid: true,
			Data: map[string]any{"name": "x", "type": "array", "of_schema": "Contributor"}},
		{Name: "select field with options", Schema: "field", Valid: true,
			Data: map[string]any{"name": "x", "type": "select", "options": []string{"a", "b"}}},
		{Name: "plain text field", Schema: "field", Valid: true,
			Data: map[string]any{"name": "x", "type": "text"}},
		// ui_node recursion: a 3-level tree through children + a named slot
		// proves the emitted Zod 4 getters resolve the self-reference at runtime.
		{Name: "ui_node nested children + slot (recursion)", Schema: "ui_node", Valid: true,
			Data: map[string]any{
				"component": "header-section/hero",
				"bind":      map[string]any{"images": "collection.projects.frames"},
				"children": []any{
					map[string]any{"component": "content/split", "children": []any{
						map[string]any{"component": "base/buttons/button", "on": map[string]any{"press": "session.signOut"}},
					}},
				},
				"slots": map[string]any{
					"footer": []any{map[string]any{"component": "footers/footer-small"}},
				},
			}},
		{Name: "ui_node missing required component", Schema: "ui_node", Valid: false,
			Data: map[string]any{"children": []any{}}},
	}

	casesJSON, err := json.Marshal(cases)
	require.NoError(t, err)

	driver := stripTypeExports(contractsTS) + "\n" + runtimeHarness(string(casesJSON))

	dir := t.TempDir()
	require.NoError(t, os.Symlink(zodNM, filepath.Join(dir, "node_modules")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "driver.mjs"), []byte(driver), 0o644))

	ctx, cancel := context.WithTimeout(context.Background(), runtimeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, nodeBin, "driver.mjs")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "emitted Zod rejected/accepted a case incorrectly:\n%s", out)
	assert.Contains(t, string(out), "OK:", "driver did not report success: %s", out)
}

// stripTypeExports drops the `export type … = z.infer<…>;` lines so the emitted
// contract module is plain ESM JavaScript, runnable by node without a
// TypeScript loader. The z.object/z.enum/superRefine declarations are already
// valid JS.
func stripTypeExports(ts string) string {
	lines := strings.Split(ts, "\n")
	kept := lines[:0]

	for _, l := range lines {
		if strings.HasPrefix(l, "export type ") {
			continue
		}

		kept = append(kept, l)
	}

	return strings.Join(kept, "\n")
}

// runtimeHarness returns the JS appended after the emitted module: it parses
// each case with the matching schema and exits non-zero on the first mismatch.
func runtimeHarness(casesJSON string) string {
	return `
const cases = ` + casesJSON + `;
const failures = [];
for (const c of cases) {
  const schema = c.schema === "collection" ? Collection : c.schema === "ui_node" ? UiNode : CollectionField;
  const res = schema.safeParse(c.data);
  if (res.success !== c.valid) {
    failures.push(c.name + ": expected valid=" + c.valid + " got valid=" + res.success);
  }
}
if (failures.length) {
  console.error("FAIL: " + JSON.stringify(failures, null, 2));
  process.exit(1);
}
console.log("OK:" + cases.length);
`
}
