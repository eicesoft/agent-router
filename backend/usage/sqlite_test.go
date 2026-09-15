package usage

import (
	"path/filepath"
	"testing"

	"agent-router/backend/storage"
)

func TestListRequestLogsPaginatesNewestFirst(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	for i := 0; i < 3; i++ {
		if err := tracker.Record(Event{
			ProviderID: "openai", ClientModel: "client-model", UpstreamModel: "gpt-test",
			RequestBody: `{"messages":[{"content":"hello"}]}`, ResponseBody: `{"choices":[]}`,
			InputTokens: i + 1, OutputTokens: i + 2, CachedInputTokens: i, ReasoningOutputTokens: i + 3, Success: true, LatencyMS: 10,
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := tracker.ListRequestLogs(2, 2, RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.TotalPages != 2 || len(page.Items) != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
	if page.Items[0].ClientModel != "client-model" || page.Items[0].RequestBody == "" || page.Items[0].CachedInputTokens != 0 || page.Items[0].ReasoningOutputTokens != 3 {
		t.Fatalf("request details were not persisted: %+v", page.Items[0])
	}
}

func TestListRequestLogsFiltersSavedNames(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	for _, event := range []Event{
		{TokenID: "key-dev", TokenName: "开发", ProviderID: "openai", ProviderName: "OpenAI", ClientModel: "chat", Success: true},
		{TokenID: "key-prod", TokenName: "生产", ProviderID: "deepseek", ProviderName: "深度求索", ClientModel: "reasoner", Success: true},
	} {
		if err := tracker.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	page, err := tracker.ListRequestLogs(1, 20, RequestLogFilter{Token: "生产", Model: "reason", Provider: "深度"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].TokenID != "key-prod" {
		t.Fatalf("unexpected filtered logs: %+v", page)
	}
}

func TestListRequestLogsFiltersStatus(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	for _, event := range []Event{
		{TokenID: "key-dev", ClientModel: "chat", Success: true},
		{TokenID: "key-prod", ClientModel: "reasoner", Success: false},
	} {
		if err := tracker.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		status string
		want   int
	}{{"failed", 1}, {"success", 1}, {"", 2}} {
		page, err := tracker.ListRequestLogs(1, 20, RequestLogFilter{Status: tc.status})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != tc.want {
			t.Fatalf("status %q: got %d logs, want %d", tc.status, page.Total, tc.want)
		}
		if tc.status != "" && len(page.Items) == 1 && page.Items[0].Success != (tc.status == "success") {
			t.Fatalf("status %q: unexpected items %+v", tc.status, page.Items)
		}
	}
}
