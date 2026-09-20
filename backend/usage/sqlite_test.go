package usage

import (
	"database/sql"
	"path/filepath"
	"strings"
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

// 统计口径必须跟随过滤条件，否则日志页的缓存率会把没命中的历史请求算进去。
func TestListRequestLogsStatsFollowFilter(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	for _, event := range []Event{
		{ProviderID: "openai", ClientModel: "chat", InputTokens: 100, OutputTokens: 10, CachedInputTokens: 80, Success: true},
		{ProviderID: "openai", ClientModel: "chat", InputTokens: 50, OutputTokens: 5, Success: false},
		{ProviderID: "deepseek", ClientModel: "reasoner", InputTokens: 7, OutputTokens: 3, Success: true},
	} {
		if err := tracker.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	page, err := tracker.ListRequestLogs(1, 20, RequestLogFilter{Provider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	stats := page.Stats
	if stats.Requests != 2 || stats.Successes != 1 || stats.InputTokens != 150 || stats.OutputTokens != 15 || stats.CachedInputTokens != 80 {
		t.Fatalf("unexpected filtered stats: %+v", stats)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items should still be the filtered page: %+v", page.Items)
	}
}

func TestUsageByKeyAndBreakdown(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	for _, event := range []Event{
		{TokenID: "key-a", TokenName: "甲", ProviderID: "openai", ClientModel: "chat", InputTokens: 10, OutputTokens: 5, Success: true},
		{TokenID: "key-a", TokenName: "甲", ProviderID: "openai", ClientModel: "chat", InputTokens: 1, OutputTokens: 2, Success: false},
		{TokenName: "乙", ProviderID: "deepseek", ClientModel: "reasoner", InputTokens: 7, OutputTokens: 3, Success: true},
	} {
		if err := tracker.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	keys := tracker.UsageByKey()
	if len(keys) != 2 {
		t.Fatalf("got %d key stats, want 2: %+v", len(keys), keys)
	}
	if keys[0].Key != "key-a" || keys[0].Requests != 2 || keys[0].Successes != 1 || keys[0].InputTokens != 11 || keys[0].OutputTokens != 7 {
		t.Fatalf("unexpected key-a stats: %+v", keys[0])
	}
	// 空 token_id 回退到 token_name 分组。
	if keys[1].Key != "乙" || keys[1].Requests != 1 {
		t.Fatalf("unexpected fallback key stats: %+v", keys[1])
	}
	byProvider := tracker.UsageByProvider()
	if len(byProvider) != 2 || byProvider[0].Key != "openai" || byProvider[0].InputTokens != 11 {
		t.Fatalf("unexpected provider stats: %+v", byProvider)
	}
}

// 使用情况页的四条聚合都跑在 request_logs 这张带大体积请求/响应体的表上。
// 一旦查询计划退化成裸扫全表，SQLite 会顺序读取每一页（含 body 溢出页），
// 生产库里就是「每次打开面板都卡几百毫秒并拉起磁盘 IO」。
func TestUsageBreakdownAvoidsFullTableScan(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	if err := tracker.Record(Event{TokenID: "key-a", TokenName: "甲", ProviderID: "openai", ClientModel: "chat", CredentialID: "cred-1", CredentialName: "主", CredentialMask: "ss****fg", Success: true, RequestBody: strings.Repeat("x", 4096)}); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{
		"keys":        usageByKeyQuery,
		"credentials": usageByCredentialQuery,
		"providers":   usageByDimensionQuery("provider_id"),
		"models":      usageByDimensionQuery("model"),
	} {
		if plan := explainPlan(t, db, query); !strings.Contains(plan, "COVERING INDEX") {
			t.Errorf("%s aggregation does not use a covering index: %s", name, plan)
		} else {
			t.Logf("%s: %s", name, strings.TrimSpace(plan))
		}
	}
}

// explainPlan renders SQLite's plan for a query as one detail-per-line text.
func explainPlan(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN " + query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan.String()
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
