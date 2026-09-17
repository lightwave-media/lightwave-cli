// Package knowledge reconciles Notion projections with durable local prints.
package knowledge

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	notionmodel "github.com/lightwave-media/lightwave-cli/internal/knowledge/generated/notion"
	syncmodel "github.com/lightwave-media/lightwave-cli/internal/knowledge/generated/sync"
)

type Page = notionmodel.NotionPageProjectionShape
type Database = notionmodel.NotionDatabaseProjectionShape
type Binding = syncmodel.ExternalReferenceIdMapOutboxAndEchoSuppression

// Content is the comparable projection, excluding provider timestamps and IDs.
// The stamped binding persists it as base_content_json/pending_content_json.
type Content struct {
	Properties map[string]json.RawMessage `json:"properties"`
	Markdown   string                     `json:"markdown"`
}

type Conflict struct {
	Field    string          `json:"field"`
	Base     json.RawMessage `json:"base,omitempty"`
	Local    json.RawMessage `json:"local,omitempty"`
	External json.RawMessage `json:"external,omitempty"`
}

//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func ContentOf(page Page) (Content, error) {
	content := Content{Properties: make(map[string]json.RawMessage)}
	if page.Markdown != nil {
		content.Markdown = *page.Markdown
	}

	if page.PropertiesJson == nil {
		return content, nil
	}

	var properties map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*page.PropertiesJson), &properties); err != nil {
		return content, fmt.Errorf("page %s properties: %w", page.NotionId, err)
	}

	for key, raw := range properties {
		var property map[string]json.RawMessage
		if err := json.Unmarshal(raw, &property); err != nil {
			return content, fmt.Errorf("property %s: %w", key, err)
		}

		delete(property, "id")
		delete(property, "has_more")

		if err := normalizeProperty(property); err != nil {
			return content, err
		}

		value, err := json.Marshal(property)
		if err != nil {
			return content, err
		}

		content.Properties[key] = value
	}

	return content, nil
}

func normalizeProperty(property map[string]json.RawMessage) error {
	var kind string
	if err := json.Unmarshal(property["type"], &kind); err != nil {
		return err
	}

	if kind == "select" || kind == "status" {
		value, err := namedOption(property[kind])
		if err != nil {
			return err
		}

		property[kind] = value

		return nil
	}

	if kind == "multi_select" || kind == "people" || kind == "relation" {
		var items []json.RawMessage
		if err := json.Unmarshal(property[kind], &items); err != nil {
			return err
		}

		for index, raw := range items {
			var err error
			if kind == "multi_select" {
				items[index], err = namedOption(raw)
			} else {
				var item struct {
					ID string `json:"id"`
				}

				err = json.Unmarshal(raw, &item)
				if err == nil {
					items[index], err = json.Marshal(item)
				}
			}

			if err != nil {
				return err
			}
		}

		sort.Slice(items, func(i, j int) bool { return string(items[i]) < string(items[j]) })
		value, err := json.Marshal(items)
		property[kind] = value

		return err
	}

	if kind != "title" && kind != "rich_text" {
		return nil
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(property[kind], &items); err != nil {
		return err
	}

	for _, item := range items {
		delete(item, "plain_text")
		delete(item, "href")
	}

	value, err := json.Marshal(items)
	property[kind] = value

	return err
}

// Option IDs and colors are provider metadata. Named choices are the stable
// write shape supported by Notion status/select properties within a data source.
func namedOption(raw json.RawMessage) (json.RawMessage, error) {
	if string(raw) == "null" {
		return raw, nil
	}

	var option map[string]json.RawMessage
	if err := json.Unmarshal(raw, &option); err != nil {
		return nil, err
	}

	if name, exists := option["name"]; exists {
		return json.Marshal(map[string]json.RawMessage{"name": name})
	}

	return json.Marshal(option)
}

// Merge compares every property independently. Absence is a value: deleting
// one field does not accidentally delete unrelated concurrent edits.
func Merge(base, local, external Content, authorities map[string]string) (Content, []Conflict) {
	result := Content{Properties: make(map[string]json.RawMessage)}
	keys := make(map[string]bool)

	for _, properties := range []map[string]json.RawMessage{base.Properties, local.Properties, external.Properties} {
		for key := range properties {
			keys[key] = true
		}
	}

	var conflicts []Conflict

	for key := range keys {
		value, conflict := mergeValue(key, base.Properties[key], local.Properties[key], external.Properties[key], authorities[key])
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
		}

		if value != nil {
			result.Properties[key] = value
		}
	}

	b := json.RawMessage(strconv.Quote(base.Markdown))
	l := json.RawMessage(strconv.Quote(local.Markdown))
	e := json.RawMessage(strconv.Quote(external.Markdown))
	value, conflict := mergeValue("markdown", b, l, e, authorities["markdown"])
	_ = json.Unmarshal(value, &result.Markdown)

	if conflict != nil {
		conflicts = append(conflicts, *conflict)
	}

	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Field < conflicts[j].Field })

	return result, conflicts
}

func mergeValue(field string, base, local, external json.RawMessage, authority string) (json.RawMessage, *Conflict) {
	if authority == "read_only" {
		if equalJSON(base, local) || equalJSON(local, external) {
			return external, nil
		}

		return local, &Conflict{Field: field, Base: base, Local: local, External: external}
	}
	// Explicitly owned runtime fields reject an external-only edit as well.
	if authority == "local" {
		return local, nil
	}

	if authority == "external" {
		return external, nil
	}

	if equalJSON(local, external) || equalJSON(base, external) {
		return local, nil
	}

	if equalJSON(base, local) {
		return external, nil
	}

	return local, &Conflict{Field: field, Base: base, Local: local, External: external}
}

func equalJSON(a, b json.RawMessage) bool {
	var left, right any

	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}

	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}

	return reflect.DeepEqual(left, right)
}

func equalContent(a, b Content) bool {
	if a.Markdown != b.Markdown || len(a.Properties) != len(b.Properties) {
		return false
	}

	for key, value := range a.Properties {
		if !equalJSON(value, b.Properties[key]) {
			return false
		}
	}

	return true
}

func delivered(before, target, actual Content) bool {
	if before.Markdown != target.Markdown && target.Markdown != actual.Markdown {
		return false
	}

	for key, value := range target.Properties {
		if !equalJSON(value, before.Properties[key]) && !equalJSON(value, actual.Properties[key]) {
			return false
		}
	}

	return true
}

func applyContent(page *Page, content Content) error {
	properties, err := json.Marshal(content.Properties)
	if err != nil {
		return err
	}

	page.PropertiesJson = ptr(string(properties))

	page.Markdown = ptr(content.Markdown)
	for _, raw := range content.Properties {
		var title struct {
			Type  string `json:"type"`
			Title []struct {
				PlainText string `json:"plain_text"`
				Text      struct {
					Content string `json:"content"`
				} `json:"text"`
			} `json:"title"`
		}
		if json.Unmarshal(raw, &title) != nil || title.Type != "title" {
			continue
		}

		page.Title = ""
		for _, item := range title.Title {
			if item.Text.Content != "" {
				page.Title += item.Text.Content
			} else {
				page.Title += item.PlainText
			}
		}
	}

	return nil
}

func ptr[T any](value T) *T { return &value }
