package zodgen

import (
	"errors"
	"fmt"
	"strings"
)

// Contract-shape emission: one Zod schema per data/ui contract file
// (ComponentContract, SectionContract, PageDefinition, SiteConfig, AppShell),
// including the cross-field rules the SST cannot express. Those rules are
// stamped as prose with ".superRefine()" handoffs (lightwave-cli#77); this
// file is where the handoff is honored.

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
    if (/(url|expression|image-set|var)\s*\(/i.test(value)) {
      ctx.addIssue({ code: "custom", path: ["tokens", key], message: "token values must not contain CSS function calls (injection guard)" });
    }
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

// EmitContracts renders contracts.generated.ts for every schema that
// declares a typescript: target. Sub-schemas emit as consts prefixed with
// the contract's TS name (PageDefinitionSeo) so contracts never collide.
func EmitContracts(schemas []*Schema, enums map[string]*EnumStamp) (string, error) {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("import { z } from \"zod\";\n")

	for _, s := range schemas {
		tsName := TSName(s.Meta.Generates)
		if tsName == "" {
			continue
		}

		shortID := shortSchemaID(s.Meta.SchemaID)

		if err := ResolveValuesRefs(s.RequiredFields, enums); err != nil {
			return "", fmt.Errorf("%s: %w", shortID, err)
		}

		if err := ResolveValuesRefs(s.OptionalFields, enums); err != nil {
			return "", fmt.Errorf("%s: %w", shortID, err)
		}

		b.WriteString("\n// ── " + s.Meta.Title + " ──\n")

		// Sub-schema consts first (referenced by the contract object).
		for _, subName := range sortedKeys(s.SubSchemas) {
			expr, err := subSchemaObject(shortID, tsName, subName, s.SubSchemas[subName], s.SubSchemas)
			if err != nil {
				return "", fmt.Errorf("%s.%s: %w", shortID, subName, err)
			}

			fmt.Fprintf(&b, "export const %s%s = %s;\n", tsName, subName, expr)
		}

		obj, err := contractObject(shortID, tsName, s)
		if err != nil {
			return "", fmt.Errorf("%s: %w", shortID, err)
		}

		fmt.Fprintf(&b, "export const %s = %s;\n", tsName, obj)
		fmt.Fprintf(&b, "export type %s = z.infer<typeof %s>;\n", tsName, tsName)
	}

	return b.String(), nil
}

// contractObject renders the top-level z.object for a contract from its
// required + optional field declarations, plus any registered refinement.
func contractObject(shortID, tsName string, s *Schema) (string, error) {
	entries := make([]string, 0, len(s.RequiredFields)+len(s.OptionalFields))

	for _, f := range s.RequiredFields {
		expr, err := contractFieldExpr(tsName, &f, s.SubSchemas)
		if err != nil {
			return "", fmt.Errorf("field %s: %w", f.Name, err)
		}

		entries = append(entries, fmt.Sprintf("%s: %s", f.Name, expr))
	}

	for _, f := range s.OptionalFields {
		expr, err := contractFieldExpr(tsName, &f, s.SubSchemas)
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

		entries = append(entries, fmt.Sprintf("%s: %s", f.Name, expr))
	}

	obj := "z.object({ " + strings.Join(entries, ", ") + " })"
	if refine, ok := contractRefinements[shortID]; ok {
		obj += refine
	}

	return obj, nil
}

// contractFieldExpr maps one contract field declaration, applying nullable
// and default chain segments. PascalCase refs resolve to prefixed consts.
func contractFieldExpr(tsName string, f *FieldDecl, subs map[string]map[string]SubField) (string, error) {
	expr, err := contractTypeExpr(tsName, f.Type, f.Options, subs)
	if err != nil {
		return "", err
	}

	if f.Nullable {
		expr += ".nullable()"
	}

	if f.Default != nil {
		expr += fmt.Sprintf(".default(%s)", tsLiteral(f.Default))
	}

	return expr, nil
}

// subSchemaObject renders one sub_schemas entry as a z.object. Sub-schema
// fields are required unless nullable; defaults chain through.
func subSchemaObject(shortID, tsName, subName string, fields map[string]SubField, subs map[string]map[string]SubField) (string, error) {
	entries := make([]string, 0, len(fields))

	for _, fieldName := range sortedKeys(fields) {
		sf := fields[fieldName]

		key := shortID + ":" + subName + "." + fieldName

		expr, ok := exprOverrides[key]
		if !ok {
			var err error

			expr, err = contractTypeExpr(tsName, sf.Type, sf.Options, subs)
			if err != nil {
				return "", fmt.Errorf("field %s: %w", fieldName, err)
			}
		}

		if mod, hasMod := fieldModifiers[key]; hasMod {
			expr += mod
		}

		if sf.Nullable {
			expr += ".nullable()"
		}

		if sf.Default != nil {
			expr += fmt.Sprintf(".default(%s)", tsLiteral(sf.Default))
		}

		entries = append(entries, fmt.Sprintf("%s: %s", fieldName, expr))
	}

	obj := "z.object({ " + strings.Join(entries, ", ") + " })"
	if refine, ok := subSchemaRefinements[shortID+":"+subName]; ok {
		obj += refine
	}

	return obj, nil
}

// contractTypeExpr is the contract-shape twin of zodExpr: PascalCase refs
// resolve to the prefixed sub-schema const instead of inline expansion.
func contractTypeExpr(tsName, t string, options []string, subs map[string]map[string]SubField) (string, error) {
	switch {
	case t == "str" || t == "date":
		return "z.string()", nil
	case t == "int":
		return "z.number().int()", nil
	case t == "float":
		return "z.number()", nil
	case t == "bool":
		return "z.boolean()", nil
	case t == "enum":
		if len(options) == 0 {
			return "", errors.New("enum without options after values_ref resolution")
		}

		vals := make([]string, len(options))
		for i, v := range options {
			vals[i] = fmt.Sprintf("%q", v)
		}

		return fmt.Sprintf("z.enum([%s])", strings.Join(vals, ", ")), nil
	case t == "dict":
		return "z.record(z.string(), z.unknown())", nil
	case strings.HasPrefix(t, "list[") && strings.HasSuffix(t, "]"):
		inner, err := contractTypeExpr(tsName, strings.TrimSuffix(strings.TrimPrefix(t, "list["), "]"), nil, subs)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("z.array(%s)", inner), nil
	case strings.HasPrefix(t, "dict[str, ") && strings.HasSuffix(t, "]"):
		inner, err := contractTypeExpr(tsName, strings.TrimSuffix(strings.TrimPrefix(t, "dict[str, "), "]"), nil, subs)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("z.record(z.string(), %s)", inner), nil
	default:
		if _, ok := subs[t]; !ok {
			return "", fmt.Errorf("unknown type %q (no sub_schemas entry)", t)
		}

		return tsName + t, nil
	}
}

// shortSchemaID extracts the trailing segment of a schema_id
// (lightwave://schemas/data/ui/page_definition → page_definition).
func shortSchemaID(schemaID string) string {
	parts := strings.Split(schemaID, "/")

	return parts[len(parts)-1]
}
