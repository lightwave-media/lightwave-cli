package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// Files holds authoritative prints, including the binding's recovery state.
// Only indexes may be discarded during a reset; these files are seed data.
const (
	privateDirectoryMode = 0o700
	privateFileMode      = 0o600
)

type Files struct{ Root string }

func (files Files) path(kind, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("invalid %s identity: %w", kind, err)
	}

	return filepath.Join(files.Root, "specs", kind, id+".yaml"), nil
}

func (files Files) LoadPage(id string) (Page, error) {
	path, err := files.path("notion_page", id)

	var page Page
	if err == nil {
		err = readPrint(path, &page)
	}

	return page, err
}

//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func (files Files) SavePage(page Page) (string, error) {
	path, err := files.path("notion_page", page.NotionId)
	if err != nil {
		return "", err
	}

	return writePrint(path, page)
}

func bindingID(id string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("notion:page:"+id)).String()
}

func (files Files) LoadBinding(id string) (Binding, error) {
	path, err := files.path("external_ref", bindingID(id))

	var binding Binding
	if err == nil {
		err = readPrint(path, &binding)
	}

	return binding, err
}

//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func (files Files) SaveBinding(binding Binding) error {
	path, err := files.path("external_ref", binding.ID.String())
	if err != nil {
		return err
	}

	_, err = writePrint(path, binding)

	return err
}

func (files Files) Bindings() ([]Binding, error) {
	return readPrints[Binding](filepath.Join(files.Root, "specs", "external_ref"))
}

func (files Files) Databases() ([]Database, error) {
	return readPrints[Database](filepath.Join(files.Root, "specs", "notion_database"))
}

// LoadDatabase reads one notion_database instance by its Notion database id
// (the file is keyed by it — CORE-0048 instances of record).
func (files Files) LoadDatabase(notionID string) (Database, error) {
	path, err := files.path("notion_database", notionID)

	var database Database
	if err == nil {
		err = readPrint(path, &database)
	}

	return database, err
}

//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func (files Files) SaveDatabase(database Database) (string, error) {
	path, err := files.path("notion_database", database.NotionId)
	if err != nil {
		return "", err
	}

	return writePrint(path, database)
}

func readPrints[T any](dir string) ([]T, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}

	items := make([]T, 0, len(paths))
	for _, path := range paths {
		var item T
		if err := readPrint(path, &item); err != nil {
			return nil, err
		}

		items = append(items, item)
	}

	return items, nil
}

func readPrint(path string, out any) error {
	if err := noSymlinks(path); err != nil {
		return err
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var value any
	if err := yaml.Unmarshal(body, &value); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, out)
}

func encodePrint(value any) ([]byte, error) {
	// Generated types declare JSON tags. Round-tripping through JSON uses the
	// stamped names rather than yaml.v3's default lowercased Go field names.
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	var mapping any
	if err := json.Unmarshal(body, &mapping); err != nil {
		return nil, err
	}

	return yaml.Marshal(mapping)
}

func writePrint(path string, value any) (string, error) {
	body, err := encodePrint(value)
	if err != nil {
		return "", err
	}

	if err := atomicWrite(path, body); err != nil {
		return "", err
	}

	return digest(body), nil
}

func digest(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func atomicWrite(path string, body []byte) error {
	if err := noSymlinks(path); err != nil {
		return err
	}

	previous, err := os.ReadFile(path)
	if err == nil && bytes.Equal(previous, body) {
		return nil
	}

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, privateDirectoryMode); err != nil {
		return err
	}

	file, err := os.CreateTemp(dir, ".notion-sync-*")
	if err != nil {
		return err
	}

	defer func() { _ = os.Remove(file.Name()) }()

	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}

	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}

	folder, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer folder.Close()

	return folder.Sync()
}

func noSymlinks(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	for current := abs; current != filepath.Dir(current); current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing sync through symlink %s", current)
		}
	}

	return nil
}

// Lock is process-scoped and released by the OS even after a crash.
func (files Files) Lock() (func(), error) {
	path := filepath.Join(files.Root, "index", "notion-sync.lock")
	if err := noSymlinks(path); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), privateDirectoryMode); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, privateFileMode)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another Notion synchronization owns this print root: %w", err)
	}

	return func() { _ = file.Close() }, nil
}

// PropertyRules retains the property-map constraints used by reconciliation.
type PropertyRules struct {
	Owners   map[string]string
	Types    map[string]string
	Required map[string]bool
}

func (rules *PropertyRules) Validate(content Content) error {
	for name, kind := range rules.Types {
		raw, exists := content.Properties[name]
		if !exists {
			if rules.Required[name] {
				return fmt.Errorf("required mapped property %s is missing", name)
			}

			continue
		}

		var property struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &property); err != nil {
			return err
		}

		if property.Type != kind {
			return fmt.Errorf("mapped property %s has type %s, expected %s", name, property.Type, kind)
		}
	}

	return nil
}

// PropertyPolicy reads ownership and required/type constraints from the stamp.
//
//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func (files Files) PropertyPolicy(database Database) (*PropertyRules, error) {
	rules := &PropertyRules{Owners: make(map[string]string), Types: make(map[string]string), Required: make(map[string]bool)}
	if database.PropertyMapRef == nil || *database.PropertyMapRef == "" {
		return rules, nil
	}

	slug := *database.PropertyMapRef
	if strings.ContainsAny(slug, `/\.`) {
		return nil, errors.New("invalid property map slug")
	}

	var mapping struct {
		DatabaseID string `json:"database_notion_id"`
		Mappings   []struct {
			Writable  *bool  `json:"writable"`
			Property  string `json:"notion_property"`
			Type      string `json:"notion_type"`
			Authority string `json:"authority"`
			Required  bool   `json:"required"`
		} `json:"mappings"`
	}
	if err := readPrint(filepath.Join(files.Root, "specs", "notion_property_map", slug+".yaml"), &mapping); err != nil {
		return nil, err
	}

	mappedID, err := uuid.Parse(mapping.DatabaseID)
	if err != nil {
		return nil, fmt.Errorf("property map database identity: %w", err)
	}

	configuredID, err := uuid.Parse(database.NotionId)
	if err != nil || mappedID != configuredID {
		return nil, errors.New("property map belongs to a different database")
	}

	for _, field := range mapping.Mappings {
		if field.Property == "" || field.Type == "" {
			return nil, errors.New("property map requires property name and type")
		}

		if _, exists := rules.Types[field.Property]; exists {
			return nil, fmt.Errorf("duplicate property mapping for %s", field.Property)
		}

		rules.Types[field.Property], rules.Required[field.Property] = field.Type, field.Required
		if field.Writable != nil && !*field.Writable {
			rules.Owners[field.Property] = "read_only"
			continue
		}

		switch field.Authority {
		case "", "reconcile", "local", "external":
			rules.Owners[field.Property] = field.Authority
		default:
			return nil, fmt.Errorf("unknown authority for %s", field.Property)
		}
	}

	return rules, nil
}
