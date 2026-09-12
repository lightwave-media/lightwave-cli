//nolint:goconst,gocritic // JSON field names repeat by shape; Server is passed by value like tools.go.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	stampKindSchemas    = "schemas"
	stampKindTemplates  = "templates"
	stampKindBlueprints = "blueprints"
	stampKindRunbooks   = "runbooks"
	stampKindWorkflows  = "workflows"
	stampKindSOPs       = "sops"
	stampKindKnowledge  = "knowledge"
	indexYAMLName       = "__index.yaml"
	maxStampReadBytes   = 64 << 10
	pathCategoryParts   = 2
	stampDescLimit      = 300
	fieldDescLimit      = 200
)

var stampKinds = []string{
	stampKindSchemas,
	stampKindTemplates,
	stampKindBlueprints,
	stampKindRunbooks,
	stampKindWorkflows,
	stampKindSOPs,
	stampKindKnowledge,
}

func libraryTools() []toolDef {
	kindDesc := strings.Join(stampKinds, ", ")

	return []toolDef{
		{Name: "stamp_list", Description: "List stamp-library entries. kind is one of: " + kindDesc + ".", InputSchema: objectSchema(map[string]any{
			"kind":     map[string]any{"type": "string", "description": "Library kind: " + kindDesc},
			"query":    map[string]any{"type": "string", "description": "Optional substring filter"},
			"category": map[string]any{"type": "string", "description": "Optional schemas category (data, interfaces, workflows, policy, integrations, servers)"},
		}, []string{"kind"})},
		{Name: "stamp_read", Description: "Read one stamp-library file by kind and path relative to that library root.", InputSchema: objectSchema(map[string]any{
			"kind": map[string]any{"type": "string"},
			"path": map[string]any{"type": "string", "description": "Relative path from the library root"},
		}, []string{"kind", "path"})},
		{Name: "schema_fields", Description: "Return required/optional fields for a schema, optionally inlining enum values.", InputSchema: objectSchema(map[string]any{
			"schema_path":   map[string]any{"type": "string", "description": "Path under src/schemas (with or without .yaml)"},
			"resolve_enums": map[string]any{"type": "string", "description": "If true, inline enum option values"},
		}, []string{"schema_path"})},
		{Name: "enum_read", Description: "Read a data/enums/<name>.yaml stamp and return its options.", InputSchema: objectSchema(map[string]any{
			"enum_name": map[string]any{"type": "string", "description": "Logical enum name, e.g. epic_statuses"},
		}, []string{"enum_name"})},
		{Name: "rules_list", Description: "List R-rules from policy/validity/core-self.yaml and core-self-continued.yaml.", InputSchema: objectSchema(map[string]any{}, nil)},
	}
}

func (s Server) stampList(args map[string]string) toolCallResult {
	kind := strings.ToLower(strings.TrimSpace(args["kind"]))
	if !validStampKind(kind) {
		return toolResult(true, fmt.Sprintf("unknown kind %q; want one of: %s", args["kind"], strings.Join(stampKinds, ", ")))
	}

	entries, err := s.listStamp(kind, args["category"])
	if err != nil {
		return toolResult(true, err.Error())
	}

	query := strings.ToLower(strings.TrimSpace(args["query"]))
	if query != "" {
		filtered := entries[:0]
		for _, e := range entries {
			blob := strings.ToLower(e.Path + " " + e.Title + " " + e.SchemaID + " " + e.Description)
			if strings.Contains(blob, query) {
				filtered = append(filtered, e)
			}
		}

		entries = filtered
	}

	return jsonResult(map[string]any{"kind": kind, "count": len(entries), "entries": entries})
}

func (s Server) stampRead(args map[string]string) toolCallResult {
	kind := strings.ToLower(strings.TrimSpace(args["kind"]))

	rel := strings.TrimSpace(args["path"])
	if !validStampKind(kind) {
		return toolResult(true, fmt.Sprintf("unknown kind %q", args["kind"]))
	}

	if rel == "" {
		return toolResult(true, "path is required")
	}

	root, err := s.libraryRoot(kind)
	if err != nil {
		return toolResult(true, err.Error())
	}

	full, err := jailJoin(root, rel)
	if err != nil {
		return toolResult(true, err.Error())
	}

	data, err := os.ReadFile(full)
	if err != nil {
		return toolResult(true, "not found: "+rel)
	}

	if len(data) > maxStampReadBytes {
		data = data[:maxStampReadBytes]
	}

	return jsonResult(map[string]any{
		"kind":  kind,
		"path":  rel,
		"bytes": len(data),
		"text":  string(data),
	})
}

func (s Server) schemaFields(args map[string]string) toolCallResult {
	doc, rel, err := s.loadSchemaDoc(args["schema_path"])
	if err != nil {
		return toolResult(true, err.Error())
	}

	resolve := truthy(args["resolve_enums"])
	meta := mapOf(doc["_meta"])
	relations := mapOf(doc["relations"])

	return jsonResult(map[string]any{
		"path":      rel,
		"schema_id": strOf(meta["schema_id"]),
		"title":     strOf(meta["title"]),
		"version":   strOf(meta["version"]),
		"required":  extractFields(doc["required_fields"], resolve, s.enumValues),
		"optional":  extractFields(doc["optional_fields"], resolve, s.enumValues),
		"relations": map[string]any{
			"parent":    relations["parent"],
			"children":  relations["children"],
			"parent_fk": relations["parent_fk"],
		},
	})
}

func (s Server) enumRead(args map[string]string) toolCallResult {
	name := strings.TrimSpace(args["enum_name"])
	if name == "" {
		return toolResult(true, "enum_name is required")
	}

	doc, _, err := s.loadSchemaDoc(filepath.Join("data", "enums", name))
	if err != nil {
		return toolResult(true, err.Error())
	}

	options := []map[string]any{}

	for _, raw := range sliceOf(doc["options"]) {
		opt := mapOf(raw)
		if len(opt) == 0 {
			continue
		}

		options = append(options, map[string]any{
			"value":       strOf(opt["value"]),
			"label":       strOf(opt["label"]),
			"description": strOf(opt["description"]),
		})
	}

	return jsonResult(map[string]any{
		"name":    strOf(doc["name"]),
		"default": strOf(doc["default"]),
		"closed":  doc["closed"],
		"options": options,
	})
}

func (s Server) rulesList() toolCallResult {
	files := []string{
		filepath.Join("policy", "validity", "core-self.yaml"),
		filepath.Join("policy", "validity", "core-self-continued.yaml"),
	}

	var rules []map[string]any

	for _, rel := range files {
		doc, _, err := s.loadSchemaDoc(rel)
		if err != nil {
			continue
		}

		for _, raw := range sliceOf(doc["rules"]) {
			rule := mapOf(raw)
			if len(rule) == 0 {
				continue
			}

			rules = append(rules, map[string]any{
				"id":          strOf(rule["id"]),
				"name":        strOf(rule["name"]),
				"severity":    strOf(rule["severity"]),
				"enforced_by": rule["enforced_by"],
			})
		}
	}

	return jsonResult(map[string]any{"count": len(rules), "rules": rules})
}

type stampEntry struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Title       string `json:"title,omitempty"`
	SchemaID    string `json:"schema_id,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
}

func (s Server) listStamp(kind, category string) ([]stampEntry, error) {
	if kind == stampKindKnowledge {
		return s.listKnowledge()
	}

	root, err := s.libraryRoot(kind)
	if err != nil {
		return nil, err
	}

	switch kind {
	case stampKindSchemas:
		return listSchemaTree(root, category)
	case stampKindTemplates:
		return listFromIndex(root, "templates", kind)
	case stampKindBlueprints:
		return listFromIndex(root, "blueprints", kind)
	case stampKindRunbooks:
		return listFromIndex(root, "categories", kind)
	case stampKindWorkflows, stampKindSOPs:
		return listFromIndex(root, "schemas", kind)
	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
}

func (s Server) listKnowledge() ([]stampEntry, error) {
	root, err := s.libraryRoot(stampKindKnowledge)
	if err != nil {
		return nil, err
	}

	var entries []stampEntry

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}

		entries = append(entries, stampEntry{Kind: stampKindKnowledge, Path: filepath.ToSlash(rel)})

		return nil
	})
	if err != nil {
		return nil, err
	}

	return entries, nil
}

func listSchemaTree(schemasRoot, category string) ([]stampEntry, error) {
	var entries []stampEntry

	err := filepath.WalkDir(schemasRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		if d.Name() == indexYAMLName || !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}

		rel, relErr := filepath.Rel(schemasRoot, path)
		if relErr != nil {
			return relErr
		}

		rel = filepath.ToSlash(rel)

		cat := strings.SplitN(rel, "/", pathCategoryParts)[0]
		if category != "" && cat != category {
			return nil
		}

		entry := stampEntry{Kind: stampKindSchemas, Path: rel, Category: cat}

		if doc, loadErr := readYAMLMap(path); loadErr == nil {
			meta := mapOf(doc["_meta"])
			entry.SchemaID = strOf(meta["schema_id"])
			entry.Title = strOf(meta["title"])
			entry.Description = truncate(strOf(meta["description"]), stampDescLimit)
		}

		entries = append(entries, entry)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return entries, nil
}

func listFromIndex(root, key, kind string) ([]stampEntry, error) {
	doc, err := readYAMLMap(filepath.Join(root, indexYAMLName))
	if err != nil {
		return nil, fmt.Errorf("read %s/__index.yaml: %w", kind, err)
	}

	var entries []stampEntry
	for _, item := range flattenIndex(doc[key], "") {
		entries = append(entries, stampEntry{Kind: kind, Path: item.path, Title: item.slug})
	}

	return entries, nil
}

type indexItem struct {
	slug string
	path string
}

func flattenIndex(v any, prefix string) []indexItem {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}

		return []indexItem{{slug: prefix, path: t}}
	case map[string]any:
		var out []indexItem

		for k, child := range t {
			next := k
			if prefix != "" {
				next = prefix + "/" + k
			}

			out = append(out, flattenIndex(child, next)...)
		}

		return out
	case map[any]any:
		converted := map[string]any{}
		for k, child := range t {
			converted[fmt.Sprint(k)] = child
		}

		return flattenIndex(converted, prefix)
	default:
		return nil
	}
}

func (s Server) loadSchemaDoc(rel string) (map[string]any, string, error) {
	root, err := s.libraryRoot(stampKindSchemas)
	if err != nil {
		return nil, "", err
	}

	rel = strings.TrimSpace(rel)

	rel = strings.TrimPrefix(rel, "/")
	if !strings.HasSuffix(rel, ".yaml") {
		rel += ".yaml"
	}

	full, err := jailJoin(root, rel)
	if err != nil {
		return nil, "", err
	}

	doc, err := readYAMLMap(full)
	if err != nil {
		return nil, "", fmt.Errorf("schema not found: %s", rel)
	}

	return doc, rel, nil
}

func (s Server) enumValues(name string) []map[string]string {
	doc, _, err := s.loadSchemaDoc(filepath.Join("data", "enums", name))
	if err != nil {
		return nil
	}

	var out []map[string]string

	for _, raw := range sliceOf(doc["options"]) {
		opt := mapOf(raw)
		if strOf(opt["value"]) == "" {
			continue
		}

		out = append(out, map[string]string{
			"value": strOf(opt["value"]),
			"label": strOf(opt["label"]),
		})
	}

	return out
}

func (s Server) libraryRoot(kind string) (string, error) {
	if kind == stampKindKnowledge {
		if s.HomeDir == "" {
			return "", errors.New("home directory is not set")
		}

		root := filepath.Join(s.HomeDir, ".lightwave", "brain", "memory")
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			return "", fmt.Errorf("knowledge base not found at %s", root)
		}

		return root, nil
	}

	if s.CoreRoot == "" {
		return "", errors.New("lightwave-core checkout is not configured")
	}

	var rel string

	switch kind {
	case stampKindSchemas:
		rel = filepath.Join("src", "schemas")
	case stampKindTemplates:
		rel = filepath.Join("src", "boilerplate", "templates")
	case stampKindBlueprints:
		rel = filepath.Join("src", "boilerplate", "blueprints")
	case stampKindRunbooks:
		rel = filepath.Join("src", "runbooks")
	case stampKindWorkflows:
		rel = filepath.Join("src", "schemas", "workflows")
	case stampKindSOPs:
		rel = filepath.Join("src", "schemas", "workflows", "sops")
	default:
		return "", fmt.Errorf("unknown kind %q", kind)
	}

	root := filepath.Join(s.CoreRoot, rel)
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", fmt.Errorf("stamp library %s not found at %s", kind, root)
	}

	return root, nil
}

func extractFields(raw any, resolve bool, enums func(string) []map[string]string) []map[string]any {
	var out []map[string]any

	for _, item := range sliceOf(raw) {
		f := mapOf(item)
		if strOf(f["name"]) == "" {
			continue
		}

		entry := map[string]any{
			"name":        strOf(f["name"]),
			"type":        strOf(f["type"]),
			"description": truncate(strOf(f["description"]), fieldDescLimit),
		}
		if resolve {
			enumRef := strOf(f["status_enum"])
			if enumRef == "" {
				enumRef = strOf(f["values_ref"])
			}

			if enumRef == "" {
				enumRef = strOf(f["priority_enum"])
			}

			if enumRef != "" {
				entry["enum_ref"] = enumRef
				entry["enum_values"] = enums(enumRef)
			}
		}

		out = append(out, entry)
	}

	return out
}

func jailJoin(root, rel string) (string, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("path %q escapes the library root", rel)
	}

	full := filepath.Join(root, clean)
	if relToRoot, err := filepath.Rel(root, full); err != nil || strings.HasPrefix(relToRoot, "..") {
		return "", fmt.Errorf("path %q escapes the library root", rel)
	}

	return full, nil
}

func validStampKind(kind string) bool {
	for _, k := range stampKinds {
		if k == kind {
			return true
		}
	}

	return false
}

func readYAMLMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	if doc == nil {
		return map[string]any{}, nil
	}

	return doc, nil
}

func jsonResult(v any) toolCallResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolResult(true, err.Error())
	}

	return toolResult(false, string(b))
}

func mapOf(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}

	return map[string]any{}
}

func sliceOf(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}

	return nil
}

func strOf(v any) string {
	if v == nil {
		return ""
	}

	if s, ok := v.(string); ok {
		return s
	}

	return fmt.Sprint(v)
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}

	return s[:n]
}
