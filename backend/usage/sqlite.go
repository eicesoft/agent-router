package usage

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Event struct {
	TokenID               string
	TokenName             string
	ProviderID            string
	ProviderName          string
	ClientModel           string
	UpstreamModel         string
	UserAgent             string
	RequestBody           string
	ResponseBody          string
	InputTokens           int
	OutputTokens          int
	CachedInputTokens     int
	ReasoningOutputTokens int
	Success               bool
	LatencyMS             int
	ErrorMessage          string
	// CredentialID/Name identify which upstream key served the request, so the
	// UI can show per-key usage without the pool needing its own counters.
	// CredentialMask is the only secret-derived value that may be persisted: the
	// short display form (ss****sfg) shown in logs instead of the raw key.
	CredentialID   string
	CredentialName string
	CredentialMask string
}

// RequestLog is a single proxied request. RequestBody and ResponseBody are the
// JSON payloads exchanged with the upstream service so the desktop UI can show
// request details without calling the provider again.
type RequestLog struct {
	ID                    int64  `json:"id"`
	CreatedAt             string `json:"createdAt"`
	TokenID               string `json:"tokenId"`
	TokenName             string `json:"tokenName"`
	ProviderID            string `json:"providerId"`
	ProviderName          string `json:"providerName"`
	ClientModel           string `json:"clientModel"`
	UpstreamModel         string `json:"upstreamModel"`
	UserAgent             string `json:"userAgent"`
	RequestBody           string `json:"requestBody"`
	ResponseBody          string `json:"responseBody"`
	InputTokens           int    `json:"inputTokens"`
	OutputTokens          int    `json:"outputTokens"`
	CachedInputTokens     int    `json:"cachedInputTokens"`
	ReasoningOutputTokens int    `json:"reasoningOutputTokens"`
	Success               bool   `json:"success"`
	LatencyMS             int    `json:"latencyMs"`
	ErrorMessage          string `json:"errorMessage"`
	CredentialID          string `json:"credentialId"`
	CredentialName        string `json:"credentialName"`
	CredentialMask        string `json:"credentialMask"`
}

type Breakdown struct {
	Providers   []UsageStat `json:"providers"`
	Models      []UsageStat `json:"models"`
	Keys        []UsageStat `json:"keys"`
	Credentials []UsageStat `json:"credentials"`
}

type RequestLogPage struct {
	Items      []RequestLog    `json:"items"`
	Page       int             `json:"page"`
	PageSize   int             `json:"pageSize"`
	Total      int             `json:"total"`
	TotalPages int             `json:"totalPages"`
	Stats      RequestLogStats `json:"stats"`
}

// RequestLogStats aggregates the rows matching the current filter, so the log
// panel can show token totals for exactly what is being searched rather than
// the whole history.
type RequestLogStats struct {
	Requests              int `json:"requests"`
	Successes             int `json:"successes"`
	InputTokens           int `json:"inputTokens"`
	OutputTokens          int `json:"outputTokens"`
	CachedInputTokens     int `json:"cachedInputTokens"`
	ReasoningOutputTokens int `json:"reasoningOutputTokens"`
}

// RequestLogFilter searches the current request history by its saved display
// identity. Each field matches either the display name or durable ID where one
// exists, so renamed/deleted providers and tokens remain discoverable.
type RequestLogFilter struct {
	Token    string `json:"token"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	// Status is "success" or "failed"; empty means no status filter.
	Status string `json:"status"`
	// From/To bound the request date (YYYY-MM-DD, compared against the UTC
	// calendar day of created_at); empty means unbounded on that side.
	From string `json:"from"`
	To   string `json:"to"`
}

type SQLiteTracker struct{ db *sql.DB }

// previewLimit caps the request/response bodies returned in log listings to a
// short preview; a page of large payloads stays within the WebView IPC message
// size and stays cheap to render. Full bodies are available on demand via
// GetRequestLog.
const previewLimit = "50"

func NewSQLiteTracker(db *sql.DB) *SQLiteTracker { return &SQLiteTracker{db: db} }
func (t *SQLiteTracker) Record(event Event) error {
	createdAt := time.Now().UTC().Format(time.RFC3339)
	tx, err := t.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO usage_events(created_at,provider_id,model,input_tokens,output_tokens,cached_input_tokens,reasoning_output_tokens,success,latency_ms,error_message) VALUES(?,?,?,?,?,?,?,?,?,?)`, createdAt, event.ProviderID, event.ClientModel, event.InputTokens, event.OutputTokens, event.CachedInputTokens, event.ReasoningOutputTokens, event.Success, event.LatencyMS, event.ErrorMessage); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO request_logs(created_at,token_id,token_name,provider_id,provider_name,client_model,upstream_model,user_agent,request_body,response_body,input_tokens,output_tokens,cached_input_tokens,reasoning_output_tokens,success,latency_ms,error_message,credential_id,credential_name,credential_mask) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, createdAt, event.TokenID, event.TokenName, event.ProviderID, event.ProviderName, event.ClientModel, event.UpstreamModel, event.UserAgent, event.RequestBody, event.ResponseBody, event.InputTokens, event.OutputTokens, event.CachedInputTokens, event.ReasoningOutputTokens, event.Success, event.LatencyMS, event.ErrorMessage, event.CredentialID, event.CredentialName, event.CredentialMask); err != nil {
		return err
	}
	return tx.Commit()
}

// GetRequestLog returns a single log with full request/response bodies for the
// detail view, which are truncated in listings to keep IPC messages small.
func (t *SQLiteTracker) GetRequestLog(id int64) (RequestLog, error) {
	var item RequestLog
	err := t.db.QueryRow(`SELECT id,created_at,token_id,token_name,provider_id,provider_name,client_model,upstream_model,user_agent,request_body,response_body,input_tokens,output_tokens,cached_input_tokens,reasoning_output_tokens,success,latency_ms,error_message,credential_id,credential_name,credential_mask FROM request_logs WHERE id = ?`, id).
		Scan(&item.ID, &item.CreatedAt, &item.TokenID, &item.TokenName, &item.ProviderID, &item.ProviderName, &item.ClientModel, &item.UpstreamModel, &item.UserAgent, &item.RequestBody, &item.ResponseBody, &item.InputTokens, &item.OutputTokens, &item.CachedInputTokens, &item.ReasoningOutputTokens, &item.Success, &item.LatencyMS, &item.ErrorMessage, &item.CredentialID, &item.CredentialName, &item.CredentialMask)
	if err != nil {
		return RequestLog{}, err
	}
	return item, nil
}

// ListRequestLogs returns the newest records first. Page numbers start at 1.
func (t *SQLiteTracker) ListRequestLogs(page, pageSize int, filter RequestLogFilter) (RequestLogPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	result := RequestLogPage{Items: []RequestLog{}, Page: page, PageSize: pageSize}
	where, args := requestLogFilterClause(filter)
	if err := t.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(success),0),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cached_input_tokens),0),COALESCE(SUM(reasoning_output_tokens),0) FROM request_logs`+where, args...).
		Scan(&result.Total, &result.Stats.Successes, &result.Stats.InputTokens, &result.Stats.OutputTokens, &result.Stats.CachedInputTokens, &result.Stats.ReasoningOutputTokens); err != nil {
		return result, fmt.Errorf("count request logs: %w", err)
	}
	result.Stats.Requests = result.Total
	result.TotalPages = (result.Total + pageSize - 1) / pageSize
	queryArgs := append(args, pageSize, (page-1)*pageSize)
	// Bodies are truncated: two requests by full trial bodies can exceed the
	// WebView IPC message size and truncate the callback JSON. The detail view
	// fetches full bodies by id via GetRequestLog.
	rows, err := t.db.Query(`SELECT id,created_at,token_id,token_name,provider_id,provider_name,client_model,upstream_model,user_agent,substr(request_body,1,`+previewLimit+`),substr(response_body,1,`+previewLimit+`),input_tokens,output_tokens,cached_input_tokens,reasoning_output_tokens,success,latency_ms,error_message,credential_id,credential_name,credential_mask FROM request_logs`+where+` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return result, fmt.Errorf("query request logs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item RequestLog
		if err := rows.Scan(&item.ID, &item.CreatedAt, &item.TokenID, &item.TokenName, &item.ProviderID, &item.ProviderName, &item.ClientModel, &item.UpstreamModel, &item.UserAgent, &item.RequestBody, &item.ResponseBody, &item.InputTokens, &item.OutputTokens, &item.CachedInputTokens, &item.ReasoningOutputTokens, &item.Success, &item.LatencyMS, &item.ErrorMessage, &item.CredentialID, &item.CredentialName, &item.CredentialMask); err != nil {
			return result, fmt.Errorf("scan request log: %w", err)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate request logs: %w", err)
	}
	return result, nil
}

func requestLogFilterClause(filter RequestLogFilter) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 6)
	if value := strings.TrimSpace(filter.Token); value != "" {
		clauses = append(clauses, `(token_id LIKE ? OR token_name LIKE ?)`)
		pattern := "%" + value + "%"
		args = append(args, pattern, pattern)
	}
	if value := strings.TrimSpace(filter.Model); value != "" {
		clauses = append(clauses, `client_model LIKE ?`)
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(filter.Provider); value != "" {
		clauses = append(clauses, `(provider_id LIKE ? OR provider_name LIKE ?)`)
		pattern := "%" + value + "%"
		args = append(args, pattern, pattern)
	}
	if value := strings.TrimSpace(filter.Status); value != "" {
		clauses = append(clauses, `success = ?`)
		args = append(args, value == "success")
	}
	// created_at is RFC3339 UTC, so ISO dates sort correctly as plain strings
	// and To is made exclusive by appending the last character < next day.
	if date := strings.TrimSpace(filter.From); date != "" {
		clauses = append(clauses, `created_at >= ?`)
		args = append(args, date+"T00:00:00Z")
	}
	if date := strings.TrimSpace(filter.To); date != "" {
		clauses = append(clauses, `created_at < ?`)
		args = append(args, nextDay(date)+"T00:00:00Z")
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// nextDay advances a YYYY-MM-DD date by one calendar day so a To filter is
// inclusive of that whole day. Unparsable input is returned unchanged, which
// simply filters nothing.
func nextDay(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

// UsageStat aggregates a dimension (provider or model) over all recorded events.
// Name is an optional display label overridden by the UI; providers and models
// carry only Key, per-key stats carry both.
type UsageStat struct {
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
	// Mask is the display form of an upstream key (ss****sfg), set only for the
	// per-credential breakdown so a deleted credential stays identifiable.
	Mask string `json:"mask,omitempty"`
	// ProviderID/Provider are set only for credential rows: the raw id comes
	// from the covering-index query, and the app layer resolves Provider as a
	// display name the same way UsageByProvider fills Name.
	ProviderID            string  `json:"providerId,omitempty"`
	Provider              string  `json:"provider,omitempty"`
	Requests              int     `json:"requests"`
	Successes             int     `json:"successes"`
	InputTokens           int     `json:"inputTokens"`
	OutputTokens          int     `json:"outputTokens"`
	CachedInputTokens     int     `json:"cachedInputTokens"`
	ReasoningOutputTokens int     `json:"reasoningOutputTokens"`
	InputCost             float64 `json:"inputCost"`
	OutputCost            float64 `json:"outputCost"`
	CacheCost             float64 `json:"cacheCost"`
	TotalCost             float64 `json:"totalCost"`
}

// Usage aggregates run on every visit to the usage panel, and request_logs
// holds multi-megabyte request/response bodies. Each query below therefore has
// to be answerable from a covering index (see backend/storage/sqlite.go): a
// plan that falls back to scanning the table reads the body overflow pages and
// turns opening the panel into hundreds of milliseconds of disk I/O.
const (
	// 每条查询都按「维度 × 路由(provider, model)」出粒度，再在 Go 里按映射单价
	// 算费并上卷到维度：同名模型在不同提供商上的单价不同，先上卷 token 再算费
	// 会把整条 failover 链按错价计。覆盖索引必须带上 provider_id/client_model。
	usageKeyRouteQuery = `SELECT COALESCE(NULLIF(token_id,''),token_name),COALESCE(NULLIF(token_name,''),'未命名密钥'),provider_id,client_model,COUNT(*),COALESCE(SUM(success),0),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cached_input_tokens),0),COALESCE(SUM(reasoning_output_tokens),0) FROM request_logs GROUP BY COALESCE(NULLIF(token_id,''),token_name),provider_id,client_model`
	// The predicate is written as two sargable comparisons rather than
	// COALESCE(credential_id,credential_name) <> '': SQLite only routes a query
	// through a covering index when the WHERE clause is indexable, and the
	// non-sargable form forces a full table scan. GROUP BY/MAX keep referring to
	// the raw columns so the grouping key stays COALESCE(NULLIF(...)).
	usageByCredentialRouteQuery = `SELECT COALESCE(NULLIF(credential_id,''),NULLIF(credential_name,'')),COALESCE(NULLIF(credential_name,''),'默认密钥'),COALESCE(MAX(NULLIF(credential_mask,'')),''),provider_id,client_model,COUNT(*),COALESCE(SUM(success),0),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cached_input_tokens),0),COALESCE(SUM(reasoning_output_tokens),0) FROM request_logs WHERE credential_id <> '' OR credential_name <> '' GROUP BY COALESCE(NULLIF(credential_id,''),credential_name),provider_id,client_model`
	usageRouteQuery             = `SELECT provider_id,model,COUNT(*),COALESCE(SUM(success),0),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cached_input_tokens),0),COALESCE(SUM(reasoning_output_tokens),0) FROM usage_events WHERE provider_id<>'' AND model<>'' GROUP BY provider_id,model`
)

func (t *SQLiteTracker) UsageByProvider(price PriceFunc) []UsageStat {
	return t.usageRoutesBy(price, true)
}
func (t *SQLiteTracker) UsageByModel(price PriceFunc) []UsageStat {
	return t.usageRoutesBy(price, false)
}

// UsageByKey aggregates request counts, tokens, and cost per local API key,
// using token_id as the durable group key and token_name as the display name.
// Key id wins over name so renamed keys stay grouped.
func (t *SQLiteTracker) UsageByKey(price PriceFunc) []UsageStat {
	rows, err := t.db.Query(usageKeyRouteQuery)
	if err != nil {
		return []UsageStat{}
	}
	defer rows.Close()
	byKey := map[string]*UsageStat{}
	order := []string{}
	for rows.Next() {
		var key, name, providerID, clientModel string
		var stat UsageStat
		if err := rows.Scan(&key, &name, &providerID, &clientModel, &stat.Requests, &stat.Successes, &stat.InputTokens, &stat.OutputTokens, &stat.CachedInputTokens, &stat.ReasoningOutputTokens); err != nil {
			return []UsageStat{}
		}
		merged, ok := byKey[key]
		if !ok {
			merged = &UsageStat{Key: key, Name: name}
			byKey[key] = merged
			order = append(order, key)
		}
		merged.Requests += stat.Requests
		merged.Successes += stat.Successes
		merged.InputTokens += stat.InputTokens
		merged.OutputTokens += stat.OutputTokens
		merged.CachedInputTokens += stat.CachedInputTokens
		merged.ReasoningOutputTokens += stat.ReasoningOutputTokens
		addCost(merged, price, providerID, clientModel, stat.InputTokens, stat.OutputTokens, stat.CachedInputTokens)
	}
	if err := rows.Err(); err != nil {
		return []UsageStat{}
	}
	return sortUsageStats(order, byKey)
}

// UsageByCredential aggregates per upstream key, using credential_id as the
// durable group key so renaming a credential keeps its history together. Rows
// recorded before a credential was identifiable group under their display name.
// MAX(credential_mask) collapses the snapshot's duplicates to one representative
// mask per group.
func (t *SQLiteTracker) UsageByCredential(price PriceFunc) []UsageStat {
	rows, err := t.db.Query(usageByCredentialRouteQuery)
	if err != nil {
		return []UsageStat{}
	}
	defer rows.Close()
	byKey := map[string]*UsageStat{}
	order := []string{}
	for rows.Next() {
		var key, name, mask, providerID, clientModel string
		var stat UsageStat
		if err := rows.Scan(&key, &name, &mask, &providerID, &clientModel, &stat.Requests, &stat.Successes, &stat.InputTokens, &stat.OutputTokens, &stat.CachedInputTokens, &stat.ReasoningOutputTokens); err != nil {
			return []UsageStat{}
		}
		merged, ok := byKey[key]
		if !ok {
			merged = &UsageStat{Key: key, Name: name, Mask: mask, ProviderID: providerID}
			byKey[key] = merged
			order = append(order, key)
		}
		if mask != "" {
			merged.Mask = mask
		}
		if providerID != "" && merged.ProviderID == "" {
			merged.ProviderID = providerID
		}
		merged.Requests += stat.Requests
		merged.Successes += stat.Successes
		merged.InputTokens += stat.InputTokens
		merged.OutputTokens += stat.OutputTokens
		merged.CachedInputTokens += stat.CachedInputTokens
		merged.ReasoningOutputTokens += stat.ReasoningOutputTokens
		addCost(merged, price, providerID, clientModel, stat.InputTokens, stat.OutputTokens, stat.CachedInputTokens)
	}
	if err := rows.Err(); err != nil {
		return []UsageStat{}
	}
	return sortUsageStats(order, byKey)
}

// usageRoutesBy reads provider×model routes once and rolls them up to either
// dimension, pricing each route before the fold so mixed-price chains bill
// correctly.
func (t *SQLiteTracker) usageRoutesBy(price PriceFunc, byProvider bool) []UsageStat {
	rows, err := t.db.Query(usageRouteQuery)
	if err != nil {
		return []UsageStat{}
	}
	defer rows.Close()
	byDim := map[string]*UsageStat{}
	order := []string{}
	for rows.Next() {
		var providerID, model string
		var stat UsageStat
		if err := rows.Scan(&providerID, &model, &stat.Requests, &stat.Successes, &stat.InputTokens, &stat.OutputTokens, &stat.CachedInputTokens, &stat.ReasoningOutputTokens); err != nil {
			return []UsageStat{}
		}
		key := providerID
		if !byProvider {
			key = model
		}
		merged, ok := byDim[key]
		if !ok {
			merged = &UsageStat{Key: key}
			byDim[key] = merged
			order = append(order, key)
		}
		merged.Requests += stat.Requests
		merged.Successes += stat.Successes
		merged.InputTokens += stat.InputTokens
		merged.OutputTokens += stat.OutputTokens
		merged.CachedInputTokens += stat.CachedInputTokens
		merged.ReasoningOutputTokens += stat.ReasoningOutputTokens
		addCost(merged, price, providerID, model, stat.InputTokens, stat.OutputTokens, stat.CachedInputTokens)
	}
	if err := rows.Err(); err != nil {
		return []UsageStat{}
	}
	return sortUsageStats(order, byDim)
}

// sortUsageStats keeps the historical order (first-seen from COUNT(*) DESC SQL)
// stable, then sorts by request count so the UI still ranks busiest first.
func sortUsageStats(order []string, byKey map[string]*UsageStat) []UsageStat {
	stats := make([]UsageStat, 0, len(order))
	for _, key := range order {
		stats = append(stats, *byKey[key])
	}
	sort.SliceStable(stats, func(i, j int) bool {
		if stats[i].Requests != stats[j].Requests {
			return stats[i].Requests > stats[j].Requests
		}
		return stats[i].Key < stats[j].Key
	})
	return stats
}

func (t *SQLiteTracker) Summary() Summary {
	var s Summary
	var successes int
	_ = t.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cached_input_tokens),0),COALESCE(SUM(reasoning_output_tokens),0),COALESCE(SUM(success),0) FROM usage_events`).Scan(&s.Requests, &s.InputTokens, &s.OutputTokens, &s.CachedInputTokens, &s.ReasoningOutputTokens, &successes)
	if s.Requests > 0 {
		s.SuccessRate = float64(successes) * 100 / float64(s.Requests)
	}
	return s
}
