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
	page, err := tracker.ListRequestLogs(2, 2, RequestLogFilter{}, nil)
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
	page, err := tracker.ListRequestLogs(1, 20, RequestLogFilter{Token: "生产", Model: "reason", Provider: "深度"}, nil)
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
	page, err := tracker.ListRequestLogs(1, 20, RequestLogFilter{Provider: "openai"}, nil)
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

// 费用必须和 token 统计一样跟随过滤条件，且按「路由」计价再上卷：
// deepseek 的账单不能混进 openai 过滤结果。
func TestListRequestLogsCostsFollowFilter(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	for _, event := range []Event{
		// openai/chat: 入 $10/M、出 $20/M、缓存 $1/M
		// uncached=20, out=5, cache=80 → 20*10 + 5*20 + 80*1
		{ProviderID: "openai", ClientModel: "chat", InputTokens: 100, OutputTokens: 5, CachedInputTokens: 80, Success: true},
		// deepseek/reasoner: 入 $1/M、出 $2/M、缓存 $0.5/M → 不应计入 openai 过滤
		{ProviderID: "deepseek", ClientModel: "reasoner", InputTokens: 7, OutputTokens: 3, CachedInputTokens: 2, Success: true},
	} {
		if err := tracker.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	price := func(providerID, _ string) (input, output, cacheRead float64) {
		switch providerID {
		case "openai":
			return 10, 20, 1
		case "deepseek":
			return 1, 2, 0.5
		}
		return 0, 0, 0
	}
	page, err := tracker.ListRequestLogs(1, 20, RequestLogFilter{Provider: "openai"}, price)
	if err != nil {
		t.Fatal(err)
	}
	const eps = 1e-12
	wantIn := float64(20*10) / 1e6
	wantOut := float64(5*20) / 1e6
	wantCache := float64(80*1) / 1e6
	stats := page.Stats
	if stats.Requests != 1 || stats.InputTokens != 100 {
		t.Fatalf("filtered stats should exclude deepseek: %+v", stats)
	}
	if d := stats.InputCost - wantIn; d > eps || d < -eps {
		t.Fatalf("input cost = %v, want %v", stats.InputCost, wantIn)
	}
	if d := stats.OutputCost - wantOut; d > eps || d < -eps {
		t.Fatalf("output cost = %v, want %v", stats.OutputCost, wantOut)
	}
	if d := stats.CacheCost - wantCache; d > eps || d < -eps {
		t.Fatalf("cache cost = %v, want %v", stats.CacheCost, wantCache)
	}
	if d := stats.TotalCost - (wantIn + wantOut + wantCache); d > eps || d < -eps {
		t.Fatalf("total cost = %v, want %v", stats.TotalCost, wantIn+wantOut+wantCache)
	}
}

// 日志统计与使用情况页一样跑在带 body 的 request_logs 上。GROUP BY 路由
// 一旦退化成裸扫，过滤查询会把溢出页全读一遍。
func TestListRequestLogsStatsAvoidsFullTableScan(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	if err := tracker.Record(Event{TokenID: "key-a", TokenName: "甲", ProviderID: "openai", ClientModel: "chat", Success: true, RequestBody: strings.Repeat("x", 4096)}); err != nil {
		t.Fatal(err)
	}
	query := `SELECT provider_id,client_model,COUNT(*),COALESCE(SUM(success),0),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cached_input_tokens),0),COALESCE(SUM(reasoning_output_tokens),0) FROM request_logs GROUP BY provider_id,client_model`
	plan := explainPlan(t, db, query)
	if !strings.Contains(plan, "COVERING INDEX") {
		t.Errorf("request log stats aggregation does not use a covering index: %s", plan)
	} else {
		t.Logf("stats: %s", strings.TrimSpace(plan))
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
		{TokenID: "key-a", TokenName: "甲", ProviderID: "openai", ClientModel: "chat", InputTokens: 10, OutputTokens: 5, CachedInputTokens: 4, Success: true},
		{TokenID: "key-a", TokenName: "甲", ProviderID: "openai", ClientModel: "chat", InputTokens: 1, OutputTokens: 2, Success: false},
		{TokenName: "乙", ProviderID: "deepseek", ClientModel: "reasoner", InputTokens: 7, OutputTokens: 3, Success: true},
	} {
		if err := tracker.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	keys := tracker.UsageByKey(nil)
	if len(keys) != 2 {
		t.Fatalf("got %d key stats, want 2: %+v", len(keys), keys)
	}
	if keys[0].Key != "key-a" || keys[0].Requests != 2 || keys[0].Successes != 1 || keys[0].InputTokens != 11 || keys[0].OutputTokens != 7 || keys[0].CachedInputTokens != 4 {
		t.Fatalf("unexpected key-a stats: %+v", keys[0])
	}
	// 空 token_id 回退到 token_name 分组。
	if keys[1].Key != "乙" || keys[1].Requests != 1 {
		t.Fatalf("unexpected fallback key stats: %+v", keys[1])
	}
	byProvider := tracker.UsageByProvider(nil)
	if len(byProvider) != 2 || byProvider[0].Key != "openai" || byProvider[0].InputTokens != 11 {
		t.Fatalf("unexpected provider stats: %+v", byProvider)
	}
}

// 上游 Key 用量要在 key 前显示提供商名，凭据行必须带出 provider_id 供上层解析。
func TestUsageByCredentialCarriesProviderID(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	if err := tracker.Record(Event{
		ProviderID: "openai", ProviderName: "OpenAI", ClientModel: "chat",
		CredentialID: "cred-1", CredentialName: "主", CredentialMask: "sk****999",
		Success: true,
	}); err != nil {
		t.Fatal(err)
	}
	stats := tracker.UsageByCredential(nil)
	if len(stats) != 1 || stats[0].ProviderID != "openai" {
		t.Fatalf("credential stats = %+v, want providerId openai", stats)
	}
}

// 计费公式：输入费扣掉缓存 token，输出费按输出 token，缓存费按缓存 token，
// 总价是三者之和。同名模型在不同提供商上单价不同时必须按各自路由计价再上卷。
func TestUsageBreakdownPricedPerRoute(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	for _, event := range []Event{
		// openai/chat: 入 $10/M、出 $20/M、缓存 $1/M
		// uncached=6, out=5, cache=4 → 6*10 + 5*20 + 4*1 全部 /1e6
		{TokenID: "key-a", ProviderID: "openai", ClientModel: "chat", InputTokens: 10, OutputTokens: 5, CachedInputTokens: 4, Success: true},
		// deepseek/reasoner: 入 $1/M、出 $2/M、缓存 $0.5/M
		// uncached=8, out=3, cache=2 → 8*1 + 3*2 + 2*0.5
		{TokenID: "key-a", ProviderID: "deepseek", ClientModel: "chat", InputTokens: 10, OutputTokens: 3, CachedInputTokens: 2, Success: true},
	} {
		if err := tracker.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	price := func(providerID, _ string) (input, output, cacheRead float64) {
		switch providerID {
		case "openai":
			return 10, 20, 1
		case "deepseek":
			return 1, 2, 0.5
		}
		return 0, 0, 0
	}
	const eps = 1e-12
	providers := tracker.UsageByProvider(price)
	if len(providers) != 2 {
		t.Fatalf("providers = %+v", providers)
	}
	// 请求多的 openai 排前（各 1 条时按 key 字典序：deepseek < openai… 都是 1，
	// 所以按 Key 升序 deepseek 在前）。逐个断言金额。
	byID := map[string]UsageStat{}
	for _, s := range providers {
		byID[s.Key] = s
	}
	openai := byID["openai"]
	wantIn := float64(6*10) / 1e6
	wantOut := float64(5*20) / 1e6
	wantCache := float64(4*1) / 1e6
	if diff := openai.InputCost - wantIn; diff > eps || diff < -eps {
		t.Fatalf("openai input cost = %v, want %v", openai.InputCost, wantIn)
	}
	if diff := openai.OutputCost - wantOut; diff > eps || diff < -eps {
		t.Fatalf("openai output cost = %v, want %v", openai.OutputCost, wantOut)
	}
	if diff := openai.CacheCost - wantCache; diff > eps || diff < -eps {
		t.Fatalf("openai cache cost = %v, want %v", openai.CacheCost, wantCache)
	}
	if diff := openai.TotalCost - (wantIn + wantOut + wantCache); diff > eps || diff < -eps {
		t.Fatalf("openai total cost = %v, want %v", openai.TotalCost, wantIn+wantOut+wantCache)
	}
	deepseek := byID["deepseek"]
	wantDeep := (float64(8*1) + float64(3*2) + float64(2*0.5)) / 1e6
	if diff := deepseek.TotalCost - wantDeep; diff > eps || diff < -eps {
		t.Fatalf("deepseek total = %v, want %v", deepseek.TotalCost, wantDeep)
	}
	// 同名模型跨提供商：费用按路由加总，而不是按某一家的单价乘总 token。
	models := tracker.UsageByModel(price)
	if len(models) != 1 || models[0].Key != "chat" {
		t.Fatalf("models = %+v", models)
	}
	wantModel := openai.TotalCost + wantDeep
	if diff := models[0].TotalCost - wantModel; diff > eps || diff < -eps {
		t.Fatalf("model total = %v, want %v", models[0].TotalCost, wantModel)
	}
	keys := tracker.UsageByKey(price)
	if len(keys) != 1 || keys[0].Key != "key-a" {
		t.Fatalf("keys = %+v", keys)
	}
	if diff := keys[0].TotalCost - wantModel; diff > eps || diff < -eps {
		t.Fatalf("key total = %v, want %v", keys[0].TotalCost, wantModel)
	}
}

// Summary 的费用与使用情况页同口径：按 provider×model 路由计价再上卷。
func TestSummaryPricedPerRoute(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "agent-router.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker := NewSQLiteTracker(db)
	for _, event := range []Event{
		// openai/chat: 入 $10/M、出 $20/M、缓存 $1/M → uncached=6, out=5, cache=4
		{ProviderID: "openai", ClientModel: "chat", InputTokens: 10, OutputTokens: 5, CachedInputTokens: 4, Success: true},
		// deepseek/reasoner: 入 $1/M、出 $2/M、缓存 $0.5/M → uncached=8, out=3, cache=2
		{ProviderID: "deepseek", ClientModel: "reasoner", InputTokens: 10, OutputTokens: 3, CachedInputTokens: 2, Success: false},
	} {
		if err := tracker.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	price := func(providerID, _ string) (input, output, cacheRead float64) {
		switch providerID {
		case "openai":
			return 10, 20, 1
		case "deepseek":
			return 1, 2, 0.5
		}
		return 0, 0, 0
	}
	s := tracker.Summary(price)
	const eps = 1e-12
	wantIn := (float64(6*10) + float64(8*1)) / 1e6
	wantOut := (float64(5*20) + float64(3*2)) / 1e6
	wantCache := (float64(4*1) + float64(2*0.5)) / 1e6
	if s.Requests != 2 || s.SuccessRate != 50 {
		t.Fatalf("counts = %+v", s)
	}
	if d := s.InputCost - wantIn; d > eps || d < -eps {
		t.Fatalf("input cost = %v, want %v", s.InputCost, wantIn)
	}
	if d := s.OutputCost - wantOut; d > eps || d < -eps {
		t.Fatalf("output cost = %v, want %v", s.OutputCost, wantOut)
	}
	if d := s.CacheCost - wantCache; d > eps || d < -eps {
		t.Fatalf("cache cost = %v, want %v", s.CacheCost, wantCache)
	}
	if d := s.CostUSD - (wantIn + wantOut + wantCache); d > eps || d < -eps {
		t.Fatalf("total cost = %v, want %v", s.CostUSD, wantIn+wantOut+wantCache)
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
		"keys":        usageKeyRouteQuery,
		"credentials": usageByCredentialRouteQuery,
		"routes":      usageRouteQuery,
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
		page, err := tracker.ListRequestLogs(1, 20, RequestLogFilter{Status: tc.status}, nil)
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
