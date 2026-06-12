//nolint:testpackage // needs internal access to generateTypes (cobra wrapper glue stays untested by design)
package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateTypesSmoke is the verification backing `codegen` in
// VerifiedCommands: the full load → parity → values_ref → emit pipeline runs
// against the fixture mirror of lightwave-core's data/ui family and produces
// both artifacts.
func TestGenerateTypesSmoke(t *testing.T) {
	t.Parallel()

	files, err := generateTypes(
		"../codegen/zodgen/testdata/ui",
		"../codegen/zodgen/testdata/enums",
	)
	require.NoError(t, err, "pipeline must run clean on the fixture mirror")

	assert.Contains(t, files["enums.generated.ts"], "export const SectionFamily = z.enum(")
	assert.Contains(t, files["sections.generated.ts"], `"content/content-section-split-image-02": contentSectionSplitImage02,`)
}
