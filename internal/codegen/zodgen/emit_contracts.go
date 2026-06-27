package zodgen

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Contract-shape emission: one Zod schema per data/ui contract file
// (ComponentContract, SectionContract, PageDefinition, SiteConfig, AppShell),
// including the cross-field rules the SST cannot express. Those rules are
// stamped as prose with ".superRefine()" handoffs (lightwave-cli#77); this
// file is where the handoff is honored.
//
// Two emit-quality behaviors (lightwave-cli#86):
//   - enum fields carrying a values_ref to a typescript-targeted enum stamp
//     reference the exported const (kind: AppShellKind) instead of inlining
//     z.enum([...]); the referenced consts are imported from enums.generated.
//   - a sub-schema declared identically in ≥2 contracts (e.g. PropField, whose
//     parity is asserted) is emitted ONCE as a bare shared const, not once
//     per contract with a prefixed name.

// exprOverrides replaces the default type mapping for specific sub-schema
// fields. Bridge until the stamp grows a `format:` key (the descriptions
// document these mappings explicitly — see ADR-0006): ISO-8601 timestamps
// declared `str` emit z.string().datetime().
var exprOverrides = map[string]string{
	"component_contract:Provenance.synced_at": "z.string().datetime()",
	"site_config:ComponentPin.synced_at":      "z.string().datetime()",
}

// fieldModifiers appends Zod chain segments to specific fields. components
// min(1): a scaffolded site with zero pinned components is semantically
// incoherent (stamped in site_config.yaml's field description).
var fieldModifiers = map[string]string{
	"site_config:UiRelease.components": ".min(1)",
}

// subSchemaRefinements appends .superRefine() to an emitted sub-schema const.
// Keyed "<schema short id>:<SubName>".
var subSchemaRefinements = map[string]string{
	// og_image: absolute https URL or media-base-relative path beginning
	// with / (page_definition.yaml Seo.og_image).
	"page_definition:Seo": `.superRefine((v, ctx) => {
  if (v.og_image != null && !(v.og_image.startsWith("https://") || v.og_image.startsWith("/"))) {
    ctx.addIssue({ code: "custom", path: ["og_image"], message: "og_image must be an absolute https URL or a media-base-relative path beginning with /" });
  }
})`,
	// CSS token injection guard (site_config.yaml Brand.tokens): keys are
	// custom-property names; values reject CSS-function injection vectors.
	"site_config:Brand": `.superRefine((v, ctx) => {
  for (const [key, value] of Object.entries(v.tokens)) {
    if (!/^--[a-z][a-z0-9-]*$/.test(key)) {
      ctx.addIssue({ code: "custom", path: ["tokens", key], message: "token keys must match ^--[a-z][a-z0-9-]*$" });
    }
    if (/(url|expression|image-set|env|attr)\s*\(/i.test(value)) {
      ctx.addIssue({ code: "custom", path: ["tokens", key], message: "token values must not contain external-data CSS functions (injection guard)" });
    }
  }
})`,
	// Collection Field cross-field rules the SST cannot express
	// (collection.yaml Field; lightwave-core#167):
	//   - type: select → options must be a non-empty list
	//   - type: array  → exactly one of of_type / of_schema is set
	//   - non-array    → neither of_type nor of_schema may be set
	"collection:Field": `.superRefine((v, ctx) => {
  if (v.type === "select" && (v.options == null || v.options.length === 0)) {
    ctx.addIssue({ code: "custom", path: ["options"], message: "select fields require a non-empty options list" });
  }
  if (v.type === "array") {
    const hasOfType = v.of_type != null;
    const hasOfSchema = v.of_schema != null;
    if (hasOfType === hasOfSchema) {
      ctx.addIssue({ code: "custom", path: ["of_type"], message: "array fields require exactly one of of_type / of_schema" });
    }
  } else if (v.of_type != null || v.of_schema != null) {
    ctx.addIssue({ code: "custom", path: ["of_type"], message: "of_type / of_schema are only valid on an array field" });
  }
})`,
}

// contractRefinements appends .superRefine() to the top-level contract.
// Keyed by schema short id.
var contractRefinements = map[string]string{
	// Legal pages require LegalMeta — the factory rejects legal pages
	// without it (page_definition.yaml).
	"page_definition": `.superRefine((v, ctx) => {
  if (v.page_type === "legal" && v.legal == null) {
    ctx.addIssue({ code: "custom", path: ["legal"], message: "legal pages require LegalMeta (jurisdiction, effective_date, required_blocks)" });
  }
})`,
	// site_config is required when kind is website (app_shell.yaml).
	"app_shell": `.superRefine((v, ctx) => {
  if (v.kind === "website" && v.site_config == null) {
    ctx.addIssue({ code: "custom", path: ["site_config"], message: "website shells require a site_config reference" });
  }
})`,
}

// minSharedDeclarations is how many typescript-targeted schemas must declare
// an identical sub-schema before it is emitted once as a bare shared const.
const minSharedDeclarations = 2

// contractEmitter carries the cross-schema state for one EmitContracts run:
// the set of sub-schema names emitted once as shared bare consts, and the set
// of enum TS const names referenced (so the import line can be emitted).
type contractEmitter struct {
	shared map[string]bool // sub-schema names emitted once as bare consts
	used   map[string]bool // enum TS const names referenced
}

// EmitContracts renders contracts.generated.ts for every schema that declares
// a typescript: target. Sub-schemas normally emit as consts prefixed with the
// contract's TS name (PageDefinitionSeo) so contracts never collide; a
// sub-schema shared identically across contracts (PropField) emits once as a
// bare const instead (lightwave-cli#86).
func EmitContracts(schemas []*Schema, enums map[string]*EnumStamp) (string, error) {
	// Resolve every values_ref up front so shared-detection compares resolved
	// shapes and enum-const references are available during emission.
	for _, s := range schemas {
		if TSName(s.Meta.Generates) == "" {
			continue
		}

		shortID := shortSchemaID(s.Meta.SchemaID)

		if err := ResolveValuesRefs(s.RequiredFields, enums); err != nil {
			return "", fmt.Errorf("%s: %w", shortID, err)
		}

		if err := ResolveValuesRefs(s.OptionalFields, enums); err != nil {
			return "", fmt.Errorf("%s: %w", shortID, err)
		}

		if err := ResolveSubSchemaValuesRefs(s.SubSchemas, enums); err != nil {
			return "", fmt.Errorf("%s: %w", shortID, err)
		}
	}

	ce := &contractEmitter{
		shared: detectSharedSubSchemas(schemas),
		used:   map[string]bool{},
	}

	var body strings.Builder

	// Shared sub-schema consts emit once, bare-named, ahead of the contracts
	// that reference them.
	if err := ce.emitSharedSubSchemas(&body, schemas); err != nil {
		return "", err
	}

	for _, s := range schemas {
		tsName := TSName(s.Meta.Generates)
		if tsName == "" {
			continue
		}

		shortID := shortSchemaID(s.Meta.SchemaID)

		body.WriteString("\n// ── " + s.Meta.Title + " ──\n")

		// Non-shared sub-schema consts (referenced by the contract object).
		for _, subName := range sortedKeys(s.SubSchemas) {
			if ce.shared[subName] {
				continue // already emitted as a bare shared const
			}

			expr, err := ce.subSchemaObject(shortID, tsName, subName, s.SubSchemas[subName], s.SubSchemas)
			if err != nil {
				return "", fmt.Errorf("%s.%s: %w", shortID, subName, err)
			}

			fmt.Fprintf(&body, "export const %s%s = %s;\n", tsName, subName, expr)
		}

		obj, err := ce.contractObject(shortID, tsName, s)
		if err != nil {
			return "", fmt.Errorf("%s: %w", shortID, err)
		}

		fmt.Fprintf(&body, "export const %s = %s;\n", tsName, obj)
		fmt.Fprintf(&body, "export type %s = z.infer<typeof %s>;\n", tsName, tsName)
	}

	return ce.assemble(body.String()), nil
}

// assemble prepends the generated header, the zod import, and (when any enum
// const was referenced) the enums.generated import to the emitted body.
func (ce *contractEmitter) assemble(body string) string {
	var b strings.Builder

	b.WriteString(generatedHeader)
	b.WriteString("import { z } from \"zod\";\n")

	if len(ce.used) > 0 {
		fmt.Fprintf(&b, "import { %s } from \"./enums.generated\";\n", strings.Join(sortedKeys(ce.used), ", "))
	}

	b.WriteString(body)

	return b.String()
}

// emitSharedSubSchemas writes each shared sub-schema once as a bare const,
// under a "Shared" header. Uses the first declaring schema's copy (parity
// guarantees the rest are identical).
func (ce *contractEmitter) emitSharedSubSchemas(body *strings.Builder, schemas []*Schema) error {
	if len(ce.shared) == 0 {
		return nil
	}

	emitted := map[string]bool{}

	body.WriteString("\n// ── Shared ──\n")

	for _, s := range schemas {
		if TSName(s.Meta.Generates) == "" {
			continue
		}

		for _, subName := range sortedKeys(s.SubSchemas) {
			if !ce.shared[subName] || emitted[subName] {
				continue
			}

			// Shared consts carry no shortID-specific override/modifier/refine
			// (detectSharedSubSchemas excludes any sub-schema that has one), so
			// the empty shortID is safe.
			expr, err := ce.subSchemaObject("", "", subName, s.SubSchemas[subName], s.SubSchemas)
			if err != nil {
				return fmt.Errorf("shared %s: %w", subName, err)
			}

			fmt.Fprintf(body, "export const %s = %s;\n", subName, expr)
			emitted[subName] = true
		}
	}

	return nil
}

// contractObject renders the top-level z.object for a contract from its
// required + optional field declarations, plus any registered refinement.
func (ce *contractEmitter) contractObject(shortID, tsName string, s *Schema) (string, error) {
	entries := make([]string, 0, len(s.RequiredFields)+len(s.OptionalFields))

	for i := range s.RequiredFields {
		f := &s.RequiredFields[i]

		expr, recursive, err := ce.contractFieldExpr(tsName, f, s.SubSchemas)
		if err != nil {
			return "", fmt.Errorf("field %s: %w", f.Name, err)
		}

		entries = append(entries, fieldEntry(f.Name, expr, recursive))
	}

	for i := range s.OptionalFields {
		f := &s.OptionalFields[i]

		expr, recursive, err := ce.contractFieldExpr(tsName, f, s.SubSchemas)
		if err != nil {
			return "", fmt.Errorf("field %s: %w", f.Name, err)
		}

		// A field with a default is already optional on input and yields a
		// non-undefined output; appending .optional() would re-widen the
		// output to T | undefined and defeat the default. contractFieldExpr
		// has already emitted the .default(...); only add .optional() when
		// there is no default to carry the absent case.
		if f.Default == nil {
			expr += ".optional()"
		}

		entries = append(entries, fieldEntry(f.Name, expr, recursive))
	}

	obj := "z.object({ " + strings.Join(entries, ", ") + " })"
	if refine, ok := contractRefinements[shortID]; ok {
		obj += refine
	}

	return obj, nil
}

// contractFieldExpr maps one contract field declaration, applying nullable
// and default chain segments. PascalCase refs resolve to prefixed consts.
func (ce *contractEmitter) contractFieldExpr(tsName string, f *FieldDecl, subs map[string]map[string]SubField) (string, bool, error) {
	expr, recursive, err := ce.contractTypeExpr(tsName, f.Type, f.EnumTSName, f.Options, subs)
	if err != nil {
		return "", false, err
	}

	if f.Nullable {
		expr += ".nullable()"
	}

	if f.Default != nil {
		expr += fmt.Sprintf(".default(%s)", tsLiteral(f.Default))
	}

	return expr, recursive, nil
}

// subSchemaObject renders one sub_schemas entry as a z.object. Sub-schema
// fields are required unless nullable; defaults chain through.
func (ce *contractEmitter) subSchemaObject(shortID, tsName, subName string, fields map[string]SubField, subs map[string]map[string]SubField) (string, error) {
	entries := make([]string, 0, len(fields))

	for _, fieldName := range sortedKeys(fields) {
		sf := fields[fieldName]

		key := shortID + ":" + subName + "." + fieldName

		expr, ok := exprOverrides[key]
		recursive := false

		if !ok {
			var err error

			expr, recursive, err = ce.contractTypeExpr(tsName, sf.Type, sf.EnumTSName, sf.Options, subs)
			if err != nil {
				return "", fmt.Errorf("field %s: %w", fieldName, err)
			}
		}

		if mod, hasMod := fieldModifiers[key]; hasMod {
			expr += mod
		}

		if sf.Nullable {
			// A nullable sub-field may be absent OR explicitly null, so emit
			// .nullable().optional() — the same shape top-level nullable+optional
			// fields already get (e.g. PageDefinition.legal). A sub-field's
			// required-ness is carried by NON-nullability; this lets records omit
			// the key entirely (collection Field.options/of_type/of_schema).
			expr += ".nullable().optional()"
		}

		if sf.Default != nil {
			expr += fmt.Sprintf(".default(%s)", tsLiteral(sf.Default))
		}

		entries = append(entries, fieldEntry(fieldName, expr, recursive))
	}

	obj := "z.object({ " + strings.Join(entries, ", ") + " })"
	if refine, ok := subSchemaRefinements[shortID+":"+subName]; ok {
		obj += refine
	}

	return obj, nil
}

// contractTypeExpr is the contract-shape twin of zodExpr: PascalCase refs
// resolve to the prefixed sub-schema const (or a bare shared const), and
// enum fields with a typescript-targeted values_ref resolve to the exported
// enum const. The second return value reports whether the expression is
// self-referential (the type names this schema's own generated type), so the
// caller can wrap the field in a getter — Zod 4's recursion form. It
// propagates up through list[...] / dict[str, ...] wrappers.
//
// enumTSName is the resolved enum const name for an enum field, or "" to
// inline z.enum([...]); it is only set on the top-level field call (list/dict
// inner calls pass "").
func (ce *contractEmitter) contractTypeExpr(tsName, t, enumTSName string, options []string, subs map[string]map[string]SubField) (string, bool, error) {
	switch {
	case t == "str" || t == "date":
		return "z.string()", false, nil
	case t == "int":
		return "z.number().int()", false, nil
	case t == "float":
		return "z.number()", false, nil
	case t == "bool":
		return "z.boolean()", false, nil
	case t == "enum":
		// Reference the exported enum const when the values_ref resolved to a
		// typescript-targeted stamp; otherwise inline (lightwave-cli#86).
		if enumTSName != "" {
			ce.used[enumTSName] = true
			return enumTSName, false, nil
		}

		if len(options) == 0 {
			return "", false, errors.New("enum without options after values_ref resolution")
		}

		vals := make([]string, len(options))
		for i, v := range options {
			vals[i] = fmt.Sprintf("%q", v)
		}

		return fmt.Sprintf("z.enum([%s])", strings.Join(vals, ", ")), false, nil
	case t == "dict":
		return "z.record(z.string(), z.unknown())", false, nil
	case strings.HasPrefix(t, "list[") && strings.HasSuffix(t, "]"):
		inner, rec, err := ce.contractTypeExpr(tsName, strings.TrimSuffix(strings.TrimPrefix(t, "list["), "]"), "", nil, subs)
		if err != nil {
			return "", false, err
		}

		return fmt.Sprintf("z.array(%s)", inner), rec, nil
	case strings.HasPrefix(t, "dict[str, ") && strings.HasSuffix(t, "]"):
		inner, rec, err := ce.contractTypeExpr(tsName, strings.TrimSuffix(strings.TrimPrefix(t, "dict[str, "), "]"), "", nil, subs)
		if err != nil {
			return "", false, err
		}

		return fmt.Sprintf("z.record(z.string(), %s)", inner), rec, nil
	case t == tsName:
		// Self-reference: the type names this schema's own generated type (e.g.
		// ui_node children: list[UiNode]). Emit a bare reference to the const;
		// the caller wraps the field in a getter so Zod 4 resolves the cycle and
		// z.infer stays correct — unlike a self-referencing z.lazy(), which
		// trips TS ts(7022) (implicit any in its own initializer) in consumers.
		return tsName, true, nil
	default:
		if _, ok := subs[t]; !ok {
			return "", false, fmt.Errorf("unknown type %q (no sub_schemas entry)", t)
		}

		// A shared sub-schema (PropField) is referenced by its bare name; an
		// ordinary one by the contract-prefixed name (lightwave-cli#86).
		if ce.shared[t] {
			return t, false, nil
		}

		return tsName + t, false, nil
	}
}

// detectSharedSubSchemas returns the set of sub-schema names that appear in
// ≥2 typescript-targeted schemas with byte-identical field maps AND no
// schema-specific override / modifier / refinement. Such a sub-schema is
// emitted once as a bare shared const instead of once per contract
// (lightwave-cli#86). PropField (parity-asserted across component_contract and
// section_contract) is the canonical case.
func detectSharedSubSchemas(schemas []*Schema) map[string]bool {
	type occurrence struct {
		fields  map[string]SubField
		shortID string
	}

	seen := map[string][]occurrence{}

	for _, s := range schemas {
		if TSName(s.Meta.Generates) == "" {
			continue
		}

		shortID := shortSchemaID(s.Meta.SchemaID)
		for subName, fields := range s.SubSchemas {
			seen[subName] = append(seen[subName], occurrence{shortID: shortID, fields: fields})
		}
	}

	shared := map[string]bool{}

	for subName, occ := range seen {
		if len(occ) < minSharedDeclarations {
			continue
		}

		if hasSchemaSpecificRule(subName, occ[0].shortID) {
			continue
		}

		identical := true

		for i := 1; i < len(occ); i++ {
			if hasSchemaSpecificRule(subName, occ[i].shortID) || !reflect.DeepEqual(occ[0].fields, occ[i].fields) {
				identical = false
				break
			}
		}

		if identical {
			shared[subName] = true
		}
	}

	return shared
}

// hasSchemaSpecificRule reports whether the named sub-schema carries any
// shortID-keyed override / modifier / refinement — such a sub-schema cannot
// be shared without dropping that behavior.
func hasSchemaSpecificRule(subName, shortID string) bool {
	if _, ok := subSchemaRefinements[shortID+":"+subName]; ok {
		return true
	}

	prefix := shortID + ":" + subName + "."
	for k := range exprOverrides {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}

	for k := range fieldModifiers {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}

	return false
}

// fieldEntry renders one z.object entry. Self-referential fields are emitted as
// getters (`get name() { return expr; }`) — Zod 4's recursion form — so the
// reference to the still-initializing const resolves lazily at access time.
func fieldEntry(name, expr string, recursive bool) string {
	if recursive {
		return fmt.Sprintf("get %s() { return %s; }", name, expr)
	}

	return fmt.Sprintf("%s: %s", name, expr)
}

// shortSchemaID extracts the trailing segment of a schema_id
// (lightwave://schemas/data/ui/page_definition → page_definition).
func shortSchemaID(schemaID string) string {
	parts := strings.Split(schemaID, "/")

	return parts[len(parts)-1]
}
