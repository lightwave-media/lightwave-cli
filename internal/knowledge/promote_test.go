package knowledge //nolint:testpackage // Drives the private transport seam and the promotion engine with fakes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pipelineID    = "pipe-1"
	readyLabel    = "Run Session Ready"
	verifiedIcon  = "✅ Verified"
	renamedStatus = "Status"
	resultsKey    = "results"
	typeKey       = "type"
)

type fakeRows []Row

func (rows fakeRows) Rows(context.Context, string) ([]Row, error) { return rows, nil }

type fakeQueue struct {
	err      error
	keys     []string
	requests []TaskRequest
}

func (queue *fakeQueue) CreateTask(_ context.Context, key string, request TaskRequest) (string, error) {
	if queue.err != nil {
		return "", queue.err
	}

	queue.keys = append(queue.keys, key)
	queue.requests = append(queue.requests, request)

	return fmt.Sprintf("task-%d", len(queue.keys)), nil
}

func taskRow(id, title, status, packet string) Row {
	properties := map[string]json.RawMessage{
		titleType:   json.RawMessage(`{"id":"title","type":"title","title":[{"type":"text","plain_text":"` + title + `"}]}`),
		statusField: json.RawMessage(`{"id":"st","type":"status","status":{"id":"1","name":"` + status + `","color":"blue"}}`),
	}
	if packet != "" {
		properties[packetField] = json.RawMessage(`{"id":"cp","type":"status","status":{"id":"2","name":"` + packet + `"}}`)
	}

	return Row{ID: id, URL: "https://www.notion.so/" + id, Properties: properties}
}

// promoteFixture lays down a bound notion_database instance and the tasks
// property map that names which Notion properties carry status and
// context_packet.
func promoteFixture(t *testing.T, mapBody string) (Files, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	files := Files{Root: root}
	databaseID := uuid.NewString()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "specs", "notion_property_map"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "specs", "notion_property_map", "tasks.yaml"),
		[]byte("slug: tasks\ndatabase_notion_id: "+databaseID+"\n"+mapBody), 0o600))
	_, err = files.SaveDatabase(Database{ID: uuid.New(), TenantID: uuid.New(), NotionId: databaseID, Title: "[DB] tasks",
		DataSourceId: ptr(uuid.NewString()), Direction: ptr("inbound"), PropertyMapRef: ptr("tasks"),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
	require.NoError(t, err)

	return files, databaseID
}

const tasksMap = `mappings:
  - notion_property: title
    notion_type: title
    local_field: title
    required: true
  - notion_property: status
    notion_type: status
    local_field: status
    required: true
  - notion_property: context_packet
    notion_type: status
    local_field: context_packet
`

func TestPromoteCreatesOnceSurfacesUnverifiedAndIsIdempotent(t *testing.T) {
	t.Parallel()
	files, databaseID := promoteFixture(t, tasksMap)
	verified, unverified, later := uuid.NewString(), uuid.NewString(), uuid.NewString()
	rows := fakeRows{
		taskRow(verified, "Ship the hop", readyLabel, verifiedIcon),
		taskRow(unverified, "Half packed", readyLabel, "In Progress"),
		taskRow(uuid.NewString(), "Not yet", "Backlog", verifiedIcon),
		taskRow(later, "Also ready", "run_session_ready", "verified"),
		taskRow(uuid.NewString(), "No packet at all", readyLabel, ""),
	}
	queue := &fakeQueue{}
	promoter := Promoter{Rows: rows, Queue: queue, Files: files}
	options := PromoteOptions{Database: databaseID, Pipeline: pipelineID}

	report, err := promoter.Run(t.Context(), options)
	require.NoError(t, err)
	require.Len(t, report.Changes, 4, "only ready rows appear; backlog rows are silent")
	assert.Equal(t, Promotion{PageID: verified, Title: "Ship the hop", Action: PromoteCreated, TaskID: "task-1"}, report.Changes[0])
	assert.Equal(t, PromoteSurfaced, report.Changes[1].Action)
	assert.Contains(t, report.Changes[1].Reason, `"in_progress"`)
	assert.Equal(t, Promotion{PageID: later, Title: "Also ready", Action: PromoteCreated, TaskID: "task-2"}, report.Changes[2])
	assert.Equal(t, PromoteSurfaced, report.Changes[3].Action)
	assert.Contains(t, report.Changes[3].Reason, "empty")

	// The request nulltickets saw carries the page as its idempotency key and binding key.
	assert.Equal(t, []string{"notion:page:" + verified, "notion:page:" + later}, queue.keys)
	assert.Equal(t, TaskRequest{PipelineID: pipelineID, Title: "Ship the hop", Description: "https://www.notion.so/" + verified,
		Metadata: map[string]string{"source": "notion", "notion_page_id": verified, "binding_key": "notion:page:" + verified}}, queue.requests[0])

	// The task is recorded as a nulltickets external_ref keyed by the page.
	binding, err := files.LoadPromotion(verified)
	require.NoError(t, err)
	assert.Equal(t, ProviderNulltickets, binding.Provider)
	assert.Equal(t, "nulltickets:task:task-1", binding.BindingKey)
	assert.Equal(t, "task", binding.ExternalType)
	assert.Equal(t, "task-1", *binding.ExternalId)
	assert.Equal(t, "notion:page:"+verified, *binding.IdempotencyKey)
	assert.Equal(t, "notion_page", binding.LocalKind)
	assert.Equal(t, verified, binding.LocalId)
	assert.Contains(t, *binding.Metadata, pipelineID)
	_, err = files.LoadPromotion(unverified)
	require.Error(t, err, "a surfaced row must not be bound")

	// Second run: nothing is sent, the report says so.
	report, err = promoter.Run(t.Context(), options)
	require.NoError(t, err)
	assert.Len(t, queue.keys, 2)
	assert.Equal(t, Promotion{PageID: verified, Title: "Ship the hop", Action: PromoteExisting, TaskID: "task-1"}, report.Changes[0])
	assert.Equal(t, PromoteExisting, report.Changes[2].Action)
}

func TestPromoteDryRunSendsNothingAndWritesNothing(t *testing.T) {
	t.Parallel()
	files, databaseID := promoteFixture(t, tasksMap)
	page := uuid.NewString()
	queue := &fakeQueue{}
	promoter := Promoter{Rows: fakeRows{taskRow(page, "Ship it", readyLabel, verifiedIcon)}, Queue: queue, Files: files}

	report, err := promoter.Run(t.Context(), PromoteOptions{Database: databaseID, Pipeline: pipelineID, DryRun: true})
	require.NoError(t, err)
	assert.True(t, report.DryRun)
	require.Len(t, report.Changes, 1)
	assert.Equal(t, PromoteDryRun, report.Changes[0].Action)
	assert.Empty(t, queue.keys)
	assert.NoDirExists(t, filepath.Join(files.Root, "specs", "external_ref"))
}

func TestPromoteRecordsQueueFailureWithoutBindingAndRetriesNextRun(t *testing.T) {
	t.Parallel()
	files, databaseID := promoteFixture(t, tasksMap)
	page := uuid.NewString()
	queue := &fakeQueue{err: errors.New("nulltickets unreachable: connection refused")}
	promoter := Promoter{Rows: fakeRows{taskRow(page, "Ship it", readyLabel, verifiedIcon)}, Queue: queue, Files: files}
	options := PromoteOptions{Database: databaseID, Pipeline: pipelineID}

	report, err := promoter.Run(t.Context(), options)
	require.ErrorContains(t, err, "1 promotion(s) failed")
	require.Len(t, report.Changes, 1)
	assert.Equal(t, PromoteError, report.Changes[0].Action)
	assert.Contains(t, report.Changes[0].Reason, "unreachable")
	_, err = files.LoadPromotion(page)
	require.Error(t, err, "a failed promotion must not be bound")

	queue.err = nil
	report, err = promoter.Run(t.Context(), options)
	require.NoError(t, err)
	assert.Equal(t, PromoteCreated, report.Changes[0].Action)
	assert.Equal(t, []string{"notion:page:" + page}, queue.keys)
}

func TestPromoteRefusesAMapThatDoesNotCarryTheGateFields(t *testing.T) {
	t.Parallel()
	files, databaseID := promoteFixture(t, "mappings:\n  - notion_property: status\n    notion_type: status\n    local_field: status\n")
	promoter := Promoter{Rows: fakeRows{}, Queue: &fakeQueue{}, Files: files}

	_, err := promoter.Run(t.Context(), PromoteOptions{Database: databaseID, Pipeline: pipelineID})
	require.ErrorContains(t, err, "context_packet")

	_, err = promoter.Run(t.Context(), PromoteOptions{Database: databaseID})
	require.ErrorContains(t, err, "pipeline id")
}

func TestPromoteResolvesPropertiesThroughTheMapNotByName(t *testing.T) {
	t.Parallel()
	renamed := `mappings:
  - notion_property: Status
    notion_type: status
    local_field: status
  - notion_property: Packet
    notion_type: status
    local_field: context_packet
`
	files, databaseID := promoteFixture(t, renamed)
	page := uuid.NewString()
	row := Row{ID: page, Properties: map[string]json.RawMessage{
		renamedStatus: json.RawMessage(`{"type":"status","status":{"name":"` + readyLabel + `"}}`),
		"Packet":      json.RawMessage(`{"type":"status","status":{"name":"Verified"}}`),
		statusField:   json.RawMessage(`{"type":"status","status":{"name":"Backlog"}}`),
	}}
	queue := &fakeQueue{}
	promoter := Promoter{Rows: fakeRows{row}, Queue: queue, Files: files}

	report, err := promoter.Run(t.Context(), PromoteOptions{Database: databaseID, Pipeline: pipelineID})
	require.NoError(t, err)
	require.Len(t, report.Changes, 1)
	assert.Equal(t, PromoteCreated, report.Changes[0].Action)
	assert.Empty(t, report.Changes[0].Title, "a row without a title property promotes with an empty title, not a crash")
}

func TestOptionSlugFoldsLabelsOntoEnumValues(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "run_session_ready", optionSlug(json.RawMessage(`{"type":"status","status":{"name":"Run Session Ready"}}`)))
	assert.Equal(t, "verified", optionSlug(json.RawMessage(`{"type":"status","status":{"name":"✅ Verified"}}`)))
	assert.Equal(t, "p0", optionSlug(json.RawMessage(`{"type":"select","select":{"name":"p0"}}`)))
	assert.Empty(t, optionSlug(json.RawMessage(`{"type":"status","status":null}`)))
	assert.Empty(t, optionSlug(nil))
}

func TestNotionRowsCarryPropertiesFromTheQuery(t *testing.T) {
	t.Parallel()
	source, first, second := uuid.NewString(), uuid.NewString(), uuid.NewString()
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/data_sources/"+source+"/query", r.URL.Path)
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		if body["start_cursor"] == nil {
			respond(t, w, map[string]any{resultsKey: []map[string]any{{"id": first, "url": "https://n/" + first,
				"properties": map[string]any{statusField: map[string]any{typeKey: statusField, statusField: map[string]string{"name": readyLabel}}}}},
				hasMoreKey: true, "next_cursor": "c2"})
			return
		}
		respond(t, w, map[string]any{resultsKey: []map[string]any{{"id": second, "last_edited_time": time.Now().UTC()}}, hasMoreKey: false})
	})

	rows, err := client.Rows(t.Context(), source)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, first, rows[0].ID)
	assert.Equal(t, "https://n/"+first, rows[0].URL)
	assert.Equal(t, "run_session_ready", optionSlug(rows[0].Properties["status"]))
	assert.Equal(t, second, rows[1].ID)

	listed, err := client.List(t.Context(), source)
	require.NoError(t, err)
	assert.Equal(t, []string{first, second}, []string{listed[0].ID, listed[1].ID})
}

func TestNullticketsCreateTaskSendsIdempotencyKeyAndBearer(t *testing.T) {
	t.Parallel()
	var seen struct {
		key, auth, path string
		body            TaskRequest
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.key, seen.auth, seen.path = r.Header.Get("Idempotency-Key"), r.Header.Get("Authorization"), r.URL.Path
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&seen.body))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"t-42"}`))
	}))
	t.Cleanup(server.Close)

	queue := NewNulltickets(server.URL+"/", "secret-bearer")
	request := TaskRequest{PipelineID: pipelineID, Title: "Ship it", Description: "https://n/p", Metadata: map[string]string{"source": "notion"}}
	id, err := queue.CreateTask(t.Context(), "notion:page:p", request)
	require.NoError(t, err)
	assert.Equal(t, "t-42", id)
	assert.Equal(t, "/tasks", seen.path)
	assert.Equal(t, "notion:page:p", seen.key)
	assert.Equal(t, "Bearer secret-bearer", seen.auth)
	assert.Equal(t, request, seen.body)
}

func TestNullticketsErrorsNeverEchoTheBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_dead_letter_stage","message":"sensitive"}}`))
	}))
	t.Cleanup(server.Close)

	queue := NewNulltickets(server.URL, "")
	_, err := queue.CreateTask(t.Context(), "k", TaskRequest{})
	require.ErrorContains(t, err, "HTTP 422")
	assert.NotContains(t, err.Error(), "sensitive")
	assert.Equal(t, DefaultNullticketsURL, NewNulltickets("", "").Base)
}
