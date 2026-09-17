package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
)

const (
	StatusPending  = "pending"
	StatusError    = "error"
	ProviderNotion = "notion"
	StatusSynced   = "synced"
	StatusDrift    = "drift"
)

type Change struct {
	PageID    string     `json:"page_id"`
	Action    string     `json:"action"`
	Error     string     `json:"error,omitempty"`
	Conflicts []Conflict `json:"conflicts,omitempty"`
	Complete  bool       `json:"content_complete"`
}

type Report struct {
	StartedAt time.Time `json:"started_at"`
	Changes   []Change  `json:"changes"`
	DryRun    bool      `json:"dry_run"`
}

type Projector interface {
	Project(context.Context, Page, Binding) error
	ProjectDatabase(context.Context, *Database) error
}

type Engine struct {
	Remote    Remote
	Projector Projector
	Files     Files
}

type Options struct {
	Database string
	DryRun   bool
	Full     bool
}

func (engine Engine) Run(ctx context.Context, options Options) (Report, error) {
	report := Report{StartedAt: time.Now().UTC(), DryRun: options.DryRun, Changes: []Change{}}
	if !options.DryRun {
		unlock, err := engine.Files.Lock()
		if err != nil {
			return report, err
		}
		defer unlock()
	}

	databases, err := engine.Files.Databases()
	if err != nil {
		return report, err
	}

	bindings, err := engine.Files.Bindings()
	if err != nil {
		return report, err
	}

	matched := false

	var failures []error

	for index := range databases {
		database := &databases[index]
		if !database.SyncEnabled {
			continue
		}

		if database.DataSourceId == nil {
			return report, fmt.Errorf("database %s has no data_source_id", database.NotionId)
		}

		if options.Database != "" && options.Database != database.NotionId && options.Database != *database.DataSourceId {
			continue
		}

		matched = true
		changes, err := engine.runDatabase(ctx, *database, bindings, options.DryRun, options.Full)

		report.Changes = append(report.Changes, changes...)
		if err != nil {
			failures = append(failures, err)
		}
	}

	if !matched {
		return report, errors.New("no enabled Notion database print matches; configure specs/notion_database with data_source_id, tenant_id and direction")
	}

	return report, errors.Join(failures...)
}

//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func (engine Engine) runDatabase(ctx context.Context, database Database, bindings []Binding, dryRun, full bool) ([]Change, error) {
	if database.TenantID == uuid.Nil {
		return nil, errors.New("notion database print requires a tenant_id")
	}

	if database.Direction == nil || (*database.Direction != "inbound" && *database.Direction != "bidirectional") {
		return nil, errors.New("notion database direction must explicitly be inbound or bidirectional")
	}

	policy, err := engine.Files.PropertyPolicy(database)
	if err != nil {
		return nil, err
	}

	if !dryRun && engine.Projector != nil {
		if err := engine.Projector.ProjectDatabase(ctx, &database); err != nil {
			return nil, err
		}
	}

	listings, err := engine.Remote.List(ctx, *database.DataSourceId)
	if err != nil {
		return nil, err
	}

	set := make(map[string]time.Time)
	for _, listing := range listings {
		set[listing.ID] = listing.Edited
	}
	// Query results can omit archived pages. Fetch known bindings separately;
	// a missing/inaccessible page is an error, never an inferred deletion.
	for index := range bindings {
		binding := &bindings[index]
		if binding.Provider != ProviderNotion || binding.ExternalId == nil {
			continue
		}

		page, err := engine.Files.LoadPage(*binding.ExternalId)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		if page.DataSourceId != nil && *page.DataSourceId == *database.DataSourceId {
			if _, found := set[page.NotionId]; !found {
				set[page.NotionId] = time.Time{}
			}
		}
	}

	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	var (
		changes  []Change
		failures []error
	)

	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return changes, err
		}

		if !full && !set[id].IsZero() {
			cached, binding, unchanged := engine.unchanged(id, set[id], database.TenantID)
			if unchanged {
				content, err := ContentOf(cached)
				if err == nil {
					err = policy.Validate(content)
				}

				if err != nil {
					failures = append(failures, err)
					changes = append(changes, Change{PageID: id, Action: StatusError, Error: err.Error()})

					continue
				}

				if !dryRun && engine.Projector != nil {
					if err := engine.Projector.Project(ctx, cached, binding); err != nil {
						failures = append(failures, err)
						changes = append(changes, Change{PageID: id, Action: StatusError, Error: err.Error()})

						continue
					}
				}

				changes = append(changes, Change{PageID: id, Action: "unchanged", Complete: cached.ContentComplete != nil && *cached.ContentComplete})

				continue
			}
		}

		change, err := engine.reconcile(ctx, database, id, policy, dryRun)
		if err != nil {
			if !dryRun {
				binding, loadErr := engine.Files.LoadBinding(id)
				if loadErr == nil && binding.TenantID == database.TenantID && binding.Provider == ProviderNotion {
					binding.SyncStatus = StatusError
					binding.LastError = ptr(err.Error())
					binding.UpdatedAt = time.Now().UTC()
					err = errors.Join(err, engine.Files.SaveBinding(binding))
				}
			}

			change.Error = err.Error()
			change.Action = StatusError

			failures = append(failures, err)
		}

		changes = append(changes, change)
	}

	return changes, errors.Join(failures...)
}

//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func (engine Engine) reconcile(ctx context.Context, database Database, id string, policy *PropertyRules, dryRun bool) (Change, error) {
	change := Change{PageID: id}

	external, err := engine.Remote.Fetch(ctx, id)
	if err != nil {
		return change, err
	}

	if external.DataSourceId == nil || *external.DataSourceId != *database.DataSourceId {
		return change, errors.New("page moved outside configured data source; retain its binding for explicit reconciliation")
	}

	external.TenantID = database.TenantID
	external.DatabaseId = ptr(database.NotionId)
	external.DataSourceId = database.DataSourceId
	change.Complete = external.ContentComplete != nil && *external.ContentComplete

	remote, err := ContentOf(external)
	if err != nil {
		return change, err
	}

	if err := policy.Validate(remote); err != nil {
		return change, err
	}

	local, localErr := engine.Files.LoadPage(id)

	binding, bindingErr := engine.Files.LoadBinding(id)
	if errors.Is(localErr, os.ErrNotExist) && errors.Is(bindingErr, os.ErrNotExist) {
		change.Action = "import"
		if dryRun {
			return change, nil
		}

		binding = newBinding(external, *database.Direction)

		binding.BaseContentJson, err = json.Marshal(remote)
		if err != nil {
			return change, err
		}

		binding.SyncStatus = StatusPending
		if err := engine.Files.SaveBinding(binding); err != nil {
			return change, err
		}

		return change, engine.accept(ctx, external, binding, remote, "adapter:notion")
	}

	// Recover a first import interrupted between its identity and page writes.
	if errors.Is(localErr, os.ErrNotExist) && bindingErr == nil && binding.LastWrittenSha256 == nil && len(binding.PendingContentJson) == 0 && binding.TenantID == database.TenantID && binding.Provider == ProviderNotion && binding.ExternalId != nil && *binding.ExternalId == id && binding.LocalId == external.ID.String() {
		change.Action = "import"
		if dryRun {
			return change, nil
		}

		return change, engine.accept(ctx, external, binding, remote, "adapter:notion")
	}

	if localErr != nil || bindingErr != nil {
		return change, fmt.Errorf("incomplete local identity pair for %s: %w", id, errors.Join(localErr, bindingErr))
	}

	if local.TenantID != database.TenantID || binding.TenantID != database.TenantID {
		return change, errors.New("binding tenant does not match configured database")
	}

	if binding.Provider != ProviderNotion || binding.ExternalId == nil || *binding.ExternalId != id || binding.LocalId != local.ID.String() || binding.PrintPath != filepath.Join("notion_page", id+".yaml") {
		return change, errors.New("binding identity does not match page print")
	}

	if binding.Direction != "inbound" && binding.Direction != "bidirectional" {
		return change, errors.New("unsupported binding direction for page reconciliation")
	}

	current, err := ContentOf(local)
	if err != nil {
		return change, err
	}

	var base Content
	if err := json.Unmarshal(binding.BaseContentJson, &base); err != nil {
		return change, fmt.Errorf("missing or invalid reconciliation base: %w", err)
	}

	effectivePolicy, err := bindingPolicy(binding.Authority, policy.Owners, base, current, remote)
	if err != nil {
		return change, err
	}

	target, conflicts := Merge(base, current, remote, effectivePolicy)
	if len(conflicts) > 0 {
		change.Action = StatusDrift

		change.Conflicts = conflicts
		if dryRun {
			return change, nil
		}

		binding.SyncStatus = StatusDrift

		detail, err := json.Marshal(conflicts)
		if err != nil {
			return change, err
		}

		binding.LastError = ptr(string(detail))

		return change, engine.Files.SaveBinding(binding)
	}

	outbound := !equalContent(target, remote)
	if outbound && external.Archived != nil && *external.Archived {
		return change, errors.New("archived page has local edits; preserve them for explicit reconciliation")
	}

	if outbound && (*database.Direction != "bidirectional" || binding.Direction != "bidirectional") {
		change.Action = StatusPending
		if dryRun {
			return change, nil
		}

		binding.SyncStatus = StatusPending
		binding.LastError = ptr("local changes retained; both database and binding must authorize bidirectional delivery")

		return change, engine.Files.SaveBinding(binding)
	}

	change.Action = "unchanged"
	if !equalContent(current, target) {
		change.Action = "import"
	}

	if outbound {
		change.Action = "export"
	}

	if dryRun {
		return change, nil
	}

	if outbound {
		if local.ContentComplete == nil || !*local.ContentComplete {
			if target.Markdown != remote.Markdown {
				return change, errors.New("local content capture is incomplete; cannot export body")
			}
		}

		binding.SyncStatus = StatusPending

		binding.PendingContentJson, err = json.Marshal(target)
		if err != nil {
			return change, err
		}

		binding.IdempotencyKey = ptr(digest(binding.PendingContentJson))
		if err := engine.Files.SaveBinding(binding); err != nil {
			return change, err
		}

		external, err = engine.Remote.Update(ctx, id, remote, target)
		if err != nil {
			binding.LastError = ptr(err.Error())
			return change, errors.Join(err, engine.Files.SaveBinding(binding))
		}

		verified, err := ContentOf(external)
		if err != nil {
			return change, err
		}

		if !delivered(remote, target, verified) {
			return change, errors.New("notion read-back differs from delivery intent; intent remains pending")
		}

		target = verified
		external.TenantID = database.TenantID
		external.DatabaseId = ptr(database.NotionId)
		external.DataSourceId = database.DataSourceId
	}
	// A writer may edit the print while the network request is in flight.
	// Preserve that newer local version and leave it for the next comparison.
	latest, err := engine.Files.LoadPage(id)
	if err != nil {
		return change, err
	}

	latestContent, err := ContentOf(latest)
	if err != nil {
		return change, err
	}

	if !equalContent(latestContent, current) {
		// Fold the concurrent local edit into the delivered version before
		// advancing the base. Otherwise old local values in unrelated fields
		// would look like intentional reversions on the following pass.
		merged, conflicts := Merge(current, latestContent, target, effectivePolicy)
		if len(conflicts) > 0 {
			change.Action, change.Conflicts = StatusDrift, conflicts
			binding.SyncStatus = StatusDrift

			detail, err := json.Marshal(conflicts)
			if err != nil {
				return change, err
			}

			binding.LastError = ptr(string(detail))

			return change, engine.Files.SaveBinding(binding)
		}

		if err := applyContent(&latest, merged); err != nil {
			return change, err
		}

		if _, err := engine.Files.SavePage(latest); err != nil {
			return change, err
		}

		binding.BaseContentJson, err = json.Marshal(target)
		if err != nil {
			return change, err
		}

		binding.PendingContentJson = nil
		binding.SyncStatus = StatusPending
		binding.LastError = ptr("local record changed during synchronization; retained for next cycle")

		return change, engine.Files.SaveBinding(binding)
	}
	// Preserve local promotion links; provider metadata is not allowed to erase them.
	external.LocalKind, external.LocalId = local.LocalKind, local.LocalId
	if err := applyContent(&external, target); err != nil {
		return change, err
	}

	origin := "adapter:notion"
	if outbound {
		origin = "local"
	}

	return change, engine.accept(ctx, external, binding, target, origin)
}

//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func newBinding(page Page, direction string) Binding {
	now := time.Now().UTC()

	return Binding{ID: uuid.MustParse(bindingID(page.NotionId)), TenantID: page.TenantID,
		BindingKey: "notion:page:" + page.NotionId, Provider: ProviderNotion, Direction: direction, Authority: "reconcile",
		LocalKind: "notion_page", LocalId: page.ID.String(), PrintPath: filepath.Join("notion_page", page.NotionId+".yaml"),
		ExternalType: "page", ExternalId: ptr(page.NotionId), ExternalUrl: ptr(page.Url),
		SyncStatus: StatusSynced, CreatedAt: now, UpdatedAt: now}
}

//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func (engine Engine) accept(ctx context.Context, page Page, binding Binding, content Content, origin string) error {
	hash, err := engine.Files.SavePage(page)
	if err != nil {
		return err
	}

	binding.BaseContentJson, err = json.Marshal(content)
	if err != nil {
		return err
	}

	binding.PendingContentJson = nil
	binding.LastWrittenSha256 = ptr(hash)
	binding.LastWriteOrigin = ptr(origin)
	binding.Etag = ptr(page.LastEditedAt.Format(time.RFC3339Nano))
	binding.LastError = nil
	binding.UpdatedAt = time.Now().UTC()
	binding.LastSyncedAt = ptr(binding.UpdatedAt)

	binding.SyncStatus = StatusSynced
	if err := engine.Files.SaveBinding(binding); err != nil {
		return err
	}

	if engine.Projector != nil {
		return engine.Projector.Project(ctx, page, binding)
	}

	return nil
}

func bindingPolicy(authority string, properties map[string]string, versions ...Content) (map[string]string, error) {
	if authority != "local" && authority != "external" && authority != "reconcile" {
		return nil, errors.New("binding requires an explicit local, external or reconcile authority")
	}

	policy := map[string]string{"markdown": authority}

	for _, version := range versions {
		for field := range version.Properties {
			policy[field] = authority
		}
	}

	for field, owner := range properties {
		policy[field] = owner
	}

	return policy, nil
}

// Enumerate every page on every pass, but download unchanged bodies only for
// --full. Missing query results still fetch their known binding, including trash.
func (engine Engine) unchanged(id string, edited time.Time, tenant uuid.UUID) (Page, Binding, bool) {
	page, err := engine.Files.LoadPage(id)
	if err != nil || page.TenantID != tenant || !page.LastEditedAt.Equal(edited) || (page.Archived != nil && *page.Archived) {
		return Page{}, Binding{}, false
	}

	binding, err := engine.Files.LoadBinding(id)
	if err != nil || binding.TenantID != tenant || binding.SyncStatus != StatusSynced || binding.LastWrittenSha256 == nil {
		return Page{}, Binding{}, false
	}

	body, err := encodePrint(page)

	return page, binding, err == nil && digest(body) == *binding.LastWrittenSha256
}
