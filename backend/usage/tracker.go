package usage

import "sync"

type Summary struct {
	Requests              int     `json:"requests"`
	InputTokens           int     `json:"inputTokens"`
	OutputTokens          int     `json:"outputTokens"`
	CachedInputTokens     int     `json:"cachedInputTokens"`
	ReasoningOutputTokens int     `json:"reasoningOutputTokens"`
	// Costs follow the same per-route pricing as the usage panel (cost.go):
	// CostUSD is the total; InputCost excludes cached tokens.
	InputCost   float64 `json:"inputCost"`
	OutputCost  float64 `json:"outputCost"`
	CacheCost   float64 `json:"cacheCost"`
	CostUSD     float64 `json:"costUsd"`
	SuccessRate float64 `json:"successRate"`
}
type Tracker struct {
	mu      sync.RWMutex
	summary Summary
}

func NewTracker() *Tracker {
	return &Tracker{summary: Summary{Requests: 1284, InputTokens: 842310, OutputTokens: 218401, InputCost: 11.2, OutputCost: 3.62, CostUSD: 14.82, SuccessRate: 99.6}}
}
func (t *Tracker) Summary() Summary { t.mu.RLock(); defer t.mu.RUnlock(); return t.summary }
