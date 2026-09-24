package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Promotion is the Notion → nulltickets hop of the pipeline: a task row that
// is run_session_ready with a verified context packet becomes exactly one
// nulltickets task. Idempotency is held twice — nulltickets keys the request
// on notion:page:<id>, and the created task is recorded locally as a
// provider=nulltickets external_ref keyed by the same page — so a second run
// is a no-op without a request. Ready rows whose packet is not verified are
// surfaced in the report and never promoted.
const (
	ProviderNulltickets   = "nulltickets"
	DefaultNullticketsURL = "http://127.0.0.1:7700"

	PromoteCreated  = "promoted"
	PromoteExisting = "already_promoted"
	PromoteSurfaced = "surfaced"
	PromoteDryRun   = "dry_run"
	PromoteError    = "error"

	readyStatus      = "run_session_ready"
	verifiedPacket   = "verified"
	statusField      = "status"
	packetField      = "context_packet"
	titleType        = "title"
	promoteAuthority = "local"
	promoteTimeout   = 5 * time.Second
	promoteOrigin    = "lw knowledge promote"
	promoteBodyCap   = 64 << 10
)

var optionSeparator = regexp.MustCompile(`[^a-z0-9]+`)

// Row is one data-source query result as Notion returns it: the properties
// come with the listing, so promotion never fetches pages one by one.
type Row struct {
	Properties map[string]json.RawMessage `json:"properties"`
	ID         string                     `json:"id"`
	URL        string                     `json:"url"`
}

type RowSource interface {
	Rows(context.Context, string) ([]Row, error)
}

type TaskRequest struct {
	Metadata    map[string]string `json:"metadata"`
	PipelineID  string            `json:"pipeline_id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
}

type TaskQueue interface {
	CreateTask(context.Context, string, TaskRequest) (string, error)
}

// Nulltickets is the smallest client the hop needs: one POST, one id back.
type Nulltickets struct {
	http  *http.Client
	Base  string
	Token string
}

func NewNulltickets(base, token string) *Nulltickets {
	if base == "" {
		base = DefaultNullticketsURL
	}

	return &Nulltickets{http: &http.Client{Timeout: promoteTimeout}, Base: strings.TrimRight(base, "/"), Token: token}
}

func (queue *Nulltickets) CreateTask(ctx context.Context, idempotencyKey string, request TaskRequest) (string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, queue.Base+"/tasks", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)

	if queue.Token != "" {
		req.Header.Set("Authorization", "Bearer "+queue.Token)
	}

	response, err := queue.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("nulltickets unreachable: %w", err)
	}

	payload, err := io.ReadAll(io.LimitReader(response.Body, promoteBodyCap))
	_ = response.Body.Close()

	if err != nil {
		return "", err
	}
	// Never echo the body: it may carry the request we just sent.
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("nulltickets POST /tasks returned HTTP %d", response.StatusCode)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &created); err != nil || created.ID == "" {
		return "", errors.New("nulltickets did not return a task id")
	}

	return created.ID, nil
}

type Promotion struct {
	PageID string `json:"page_id"`
	Title  string `json:"title"`
	Action string `json:"action"`
	TaskID string `json:"task_id,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type PromoteReport struct {
	StartedAt time.Time   `json:"started_at"`
	Database  string      `json:"database"`
	Pipeline  string      `json:"pipeline"`
	Changes   []Promotion `json:"changes"`
	DryRun    bool        `json:"dry_run"`
}

type PromoteOptions struct {
	Database string
	Pipeline string
	DryRun   bool
}

type Promoter struct {
	Rows  RowSource
	Queue TaskQueue
	Files Files
}

func (promoter Promoter) Run(ctx context.Context, options PromoteOptions) (PromoteReport, error) {
	report := PromoteReport{StartedAt: time.Now().UTC(), Database: options.Database, Pipeline: options.Pipeline, DryRun: options.DryRun, Changes: []Promotion{}}

	if options.Pipeline == "" {
		return report, errors.New("promotion requires the nulltickets pipeline id")
	}

	database, err := promoter.Files.LoadDatabase(options.Database)
	if err != nil {
		return report, err
	}

	if database.DataSourceId == nil || *database.DataSourceId == "" {
		return report, errors.New("notion database print has no data_source_id; run lw knowledge sync first")
	}

	fields, err := promoter.Files.PropertyFields(database)
	if err != nil {
		return report, err
	}

	statusProperty, packetProperty := fields[statusField], fields[packetField]
	if statusProperty == "" || packetProperty == "" {
		return report, fmt.Errorf("property map %q must map local fields %s and %s", stringOr(database.PropertyMapRef), statusField, packetField)
	}

	if !options.DryRun {
		unlock, err := promoter.Files.Lock()
		if err != nil {
			return report, err
		}
		defer unlock()
	}

	rows, err := promoter.Rows.Rows(ctx, *database.DataSourceId)
	if err != nil {
		return report, err
	}

	failed := 0

	for _, row := range rows {
		if optionSlug(row.Properties[statusProperty]) != readyStatus {
			continue
		}

		change := promoter.promote(ctx, database, row, optionSlug(row.Properties[packetProperty]), options)
		if change.Action == PromoteError {
			failed++
		}

		report.Changes = append(report.Changes, change)
	}

	if failed > 0 {
		return report, fmt.Errorf("%d promotion(s) failed; nulltickets keeps them idempotent for the next run", failed)
	}

	return report, nil
}

//nolint:gocritic // Value snapshots isolate the hop from mutations of generated records.
func (promoter Promoter) promote(ctx context.Context, database Database, row Row, packet string, options PromoteOptions) Promotion {
	change := Promotion{PageID: row.ID, Title: plainText(row.Properties)}

	if packet != verifiedPacket {
		change.Action, change.Reason = PromoteSurfaced, packetField+" is "+quoteOr(packet)+", not "+verifiedPacket

		return change
	}

	if existing, err := promoter.Files.LoadPromotion(row.ID); err == nil {
		change.Action, change.TaskID = PromoteExisting, stringOr(existing.ExternalId)

		return change
	}

	if options.DryRun {
		change.Action = PromoteDryRun

		return change
	}

	idempotencyKey := "notion:page:" + row.ID
	request := TaskRequest{PipelineID: options.Pipeline, Title: change.Title, Description: row.URL,
		Metadata: map[string]string{"source": ProviderNotion, "notion_page_id": row.ID, "binding_key": idempotencyKey}}

	taskID, err := promoter.Queue.CreateTask(ctx, idempotencyKey, request)
	if err != nil {
		change.Action, change.Reason = PromoteError, err.Error()

		return change
	}

	if err := promoter.Files.SaveBinding(promotionBinding(database, row, taskID, options.Pipeline)); err != nil {
		change.Action, change.Reason, change.TaskID = PromoteError, "task created but binding not written: "+err.Error(), taskID

		return change
	}

	change.Action, change.TaskID = PromoteCreated, taskID

	return change
}

func promotionID(pageID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("nulltickets:task:notion:page:"+pageID)).String()
}

// LoadPromotion reads the external_ref that records a page's nulltickets task.
func (files Files) LoadPromotion(pageID string) (Binding, error) {
	path, err := files.path("external_ref", promotionID(pageID))

	var binding Binding
	if err == nil {
		err = readPrint(path, &binding)
	}

	return binding, err
}

//nolint:gocritic // Value snapshots isolate the hop from mutations of generated records.
func promotionBinding(database Database, row Row, taskID, pipeline string) Binding {
	now := time.Now().UTC()
	metadata := fmt.Sprintf(`{"pipeline_id":%q,"notion_database_id":%q,"notion_url":%q}`, pipeline, database.NotionId, row.URL)

	return Binding{ID: uuid.MustParse(promotionID(row.ID)), TenantID: database.TenantID,
		BindingKey: "nulltickets:task:" + taskID, Provider: ProviderNulltickets, Direction: "outbound", Authority: promoteAuthority,
		LocalKind: "notion_page", LocalId: row.ID, PrintPath: filepath.Join("notion_page", row.ID+".yaml"),
		ExternalType: "task", ExternalId: ptr(taskID), IdempotencyKey: ptr("notion:page:" + row.ID),
		LastWriteOrigin: ptr(promoteOrigin), Metadata: ptr(metadata),
		SyncStatus: StatusSynced, CreatedAt: now, UpdatedAt: now, LastSyncedAt: ptr(now)}
}

// optionSlug folds a Notion status/select option label onto the enum
// vocabulary: "Run Session Ready" and "✅ Verified" compare as
// run_session_ready and verified, so promotion works before and after the
// option labels are renamed to the bare values.
func optionSlug(raw json.RawMessage) string {
	var property struct {
		Status *struct {
			Name string `json:"name"`
		} `json:"status"`
		Select *struct {
			Name string `json:"name"`
		} `json:"select"`
	}
	if json.Unmarshal(raw, &property) != nil {
		return ""
	}

	name := ""

	switch {
	case property.Status != nil:
		name = property.Status.Name
	case property.Select != nil:
		name = property.Select.Name
	}

	return strings.Trim(optionSeparator.ReplaceAllString(strings.ToLower(name), "_"), "_")
}

func plainText(properties map[string]json.RawMessage) string {
	for _, raw := range properties {
		var title struct {
			Type  string `json:"type"`
			Title []struct {
				PlainText string `json:"plain_text"`
			} `json:"title"`
		}
		if json.Unmarshal(raw, &title) != nil || title.Type != titleType {
			continue
		}

		var text strings.Builder
		for _, item := range title.Title {
			text.WriteString(item.PlainText)
		}

		return text.String()
	}

	return ""
}

func stringOr(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func quoteOr(value string) string {
	if value == "" {
		return "empty"
	}

	return fmt.Sprintf("%q", value)
}
