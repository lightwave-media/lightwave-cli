package zodgen_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/codegen/zodgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite golden files from emitter output")

func loadFixtures(t *testing.T) (component, section *zodgen.Schema, enums map[string]*zodgen.EnumStamp) {
	t.Helper()
	var err error
	component, err = zodgen.LoadSchema(filepath.Join("testdata", "ui", "component_contract.yaml"))
	require.NoError(t, err, "loading component fixture")
	section, err = zodgen.LoadSchema(filepath.Join("testdata", "ui", "section_contract.yaml"))
	require.NoError(t, err, "loading section fixture")
	enums, err = zodgen.LoadEnums(filepath.Join("testdata", "enums"))
	require.NoError(t, err, "loading enum fixtures")
	return component, section, enums
}

func TestSectionRoundTripGolden(t *testing.T) {
	t.Parallel()
	_, section, _ := loadFixtures(t)

	fixture, err := zodgen.SectionInstanceFromExample(section)
	require.NoError(t, err, "decoding round-trip fixture from example block")

	got, err := zodgen.EmitSections([]*zodgen.SectionInstance{fixture})
	require.NoError(t, err, "emitting sections")

	golden := filepath.Join("testdata", "golden", "sections.generated.ts")
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "reading golden (run with -update to regenerate)")
	assert.Equal(t, string(want), got, "emitted sections must match golden — the golden mirrors joelschaeffer-site src/data/sections.ts semantics")
}

// TestSectionRoundTripShapes pins the semantic core of the round-trip
// acceptance (lightwave-cli#77): every prop expression the hand-written
// sections.ts registry declares for content-section-split-image-02 must
// appear in the generated Zod.
func TestSectionRoundTripShapes(t *testing.T) {
	t.Parallel()
	_, section, _ := loadFixtures(t)
	fixture, err := zodgen.SectionInstanceFromExample(section)
	require.NoError(t, err)
	got, err := zodgen.EmitSections([]*zodgen.SectionInstance{fixture})
	require.NoError(t, err)

	for _, want := range []string{
		`eyebrow: z.string().optional()`,
		`title: z.string(),`,
		`paragraphs: z.array(z.string()).optional()`,
		`actions: z.array(z.object({ label: z.string(), href: z.string(), color: z.enum(["primary", "secondary"]).optional() })).optional()`,
		`image: z.string(),`,
		`imageCaption: z.object({ title: z.string(), role: z.string().optional(), subrole: z.string().optional(), rating: z.number().optional() }).optional()`,
		`"content/content-section-split-image-02": contentSectionSplitImage02,`,
	} {
		assert.Contains(t, got, want)
	}
}

func TestPropFieldParity(t *testing.T) {
	t.Parallel()
	component, section, _ := loadFixtures(t)

	require.NoError(t, zodgen.CheckPropFieldParity(component, section), "identical fixtures must pass")

	drifted := *section
	drifted.SubSchemas = map[string]map[string]zodgen.SubField{
		"PropField": {"name": {Type: "str"}},
	}
	err := zodgen.CheckPropFieldParity(component, &drifted)
	require.Error(t, err, "drifted PropField must fail generation")
	assert.Contains(t, err.Error(), "parity violated")
}

func TestResolveValuesRefs(t *testing.T) {
	t.Parallel()
	_, section, enums := loadFixtures(t)

	require.NoError(t, zodgen.ResolveValuesRefs(section.RequiredFields, enums))
	var family *zodgen.FieldDecl
	for i := range section.RequiredFields {
		if section.RequiredFields[i].Name == "family" {
			family = &section.RequiredFields[i]
		}
	}
	require.NotNil(t, family)
	assert.Equal(t,
		[]string{"header-section", "header-navigation", "content", "footers", "blog", "store", "photography"},
		family.Options, "values_ref must resolve to the enum stamp's values in order")

	missing := []zodgen.FieldDecl{{Name: "family", Type: "enum", ValuesRef: "nope"}}
	err := zodgen.ResolveValuesRefs(missing, enums)
	require.Error(t, err, "missing enum stamp must error, not emit z.enum([])")
	assert.Contains(t, err.Error(), "nope")
}

func TestEmitContractsGolden(t *testing.T) {
	t.Parallel()
	component, section, enums := loadFixtures(t)

	extra := make([]*zodgen.Schema, 0, 3)

	for _, name := range []string{"page_definition.yaml", "site_config.yaml", "app_shell.yaml"} {
		s, err := zodgen.LoadSchema(filepath.Join("testdata", "ui", name))
		require.NoError(t, err, name)

		extra = append(extra, s)
	}

	got, err := zodgen.EmitContracts(append([]*zodgen.Schema{component, section}, extra...), enums)
	require.NoError(t, err, "emitting contracts")

	golden := filepath.Join("testdata", "golden", "contracts.generated.ts")
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o644))
	}

	want, err := os.ReadFile(golden)
	require.NoError(t, err, "reading golden (run with -update to regenerate)")
	assert.Equal(t, string(want), got)
}

// TestEmitContractsEnforcement pins every #77 rule the stamp hands off to
// the emitter: the two cross-field superRefines, the og_image form, the CSS
// token guard, components min(1), and the datetime override.
func TestEmitContractsEnforcement(t *testing.T) {
	t.Parallel()
	component, section, enums := loadFixtures(t)

	extra := make([]*zodgen.Schema, 0, 3)

	for _, name := range []string{"page_definition.yaml", "site_config.yaml", "app_shell.yaml"} {
		s, err := zodgen.LoadSchema(filepath.Join("testdata", "ui", name))
		require.NoError(t, err, name)

		extra = append(extra, s)
	}

	got, err := zodgen.EmitContracts(append([]*zodgen.Schema{component, section}, extra...), enums)
	require.NoError(t, err)

	for _, want := range []string{
		`if (v.page_type === "legal" && v.legal == null)`,
		`if (v.kind === "website" && v.site_config == null)`,
		`og_image must be an absolute https URL`,
		`token values must not contain CSS function calls`,
		`z.array(SiteConfigComponentPin).min(1)`,
		`synced_at: z.string().datetime()`,
		// last field of SiteConfig — the trailing " })" proves no .optional()
		// was re-appended after the default.
		`locale: z.string().default("en-GB") })`,
		`no_index: z.boolean().default(false)`,
		`export type PageDefinition = z.infer<typeof PageDefinition>;`,
	} {
		assert.Contains(t, got, want)
	}
}

func TestEmitEnums(t *testing.T) {
	t.Parallel()
	_, _, enums := loadFixtures(t)
	got := zodgen.EmitEnums(enums)
	assert.Contains(t, got, `export const SectionFamily = z.enum(["header-section", "header-navigation", "content", "footers", "blog", "store", "photography"]);`)
	assert.Contains(t, got, "export type SectionFamily = z.infer<typeof SectionFamily>;")
}

func TestEmitSectionsErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		want  string
		props []zodgen.FieldDecl
	}{
		{
			name:  "unknown prop type ref",
			props: []zodgen.FieldDecl{{Name: "a", Type: "Missing"}},
			want:  "unknown type",
		},
		{
			name:  "enum prop without options",
			props: []zodgen.FieldDecl{{Name: "a", Type: "enum"}},
			want:  "enum without options",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inst := &zodgen.SectionInstance{Key: "content/x", Variant: "x", Props: tt.props}
			_, err := zodgen.EmitSections([]*zodgen.SectionInstance{inst})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
