package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ModelMapping routes one client model name to a provider's upstream model.
//
// Aliases are extra client-side names that address the same route. They are
// deliberately kept out of List Models and out of the client configs the
// templates generate: an alias is a convenience for a caller that already knows
// the name, not a second model the gateway advertises.
type ModelMapping struct {
	ID            string   `json:"id"`
	ClientModel   string   `json:"clientModel"`
	ProviderID    string   `json:"providerId"`
	UpstreamModel string   `json:"upstreamModel"`
	Aliases       []string `json:"aliases"`
	Enabled       bool     `json:"enabled"`
	// 模型元数据，供 UI 配置展示与客户端配置生成（pi/omp 的 contextWindow/
	// maxTokens/input，ai-sdk 系的 limit/modalities/attachment）消费，网关路由
	// 不消费。大小单位为 tokens，0 表示未设置。
	// InputTypes 取值为 text/image/audio/video/file。
	InputTypes       []string `json:"inputTypes"`
	InputContextSize int      `json:"inputContextSize"`
	OutputSize       int      `json:"outputSize"`
	// 价格仅存储与展示，单位 USD / 百万 tokens，默认 0（未设置）；网关不据此计费。
	InputPrice     float64 `json:"inputPrice"`
	OutputPrice    float64 `json:"outputPrice"`
	CacheReadPrice float64 `json:"cacheReadPrice"`
}

// Names returns every client-side name that resolves to this mapping.
func (m ModelMapping) Names() []string {
	out := make([]string, 0, len(m.Aliases)+1)
	if m.ClientModel != "" {
		out = append(out, m.ClientModel)
	}
	out = append(out, m.Aliases...)
	return out
}

type MappingStore struct {
	mu        sync.RWMutex
	db        *sql.DB
	items     map[string]ModelMapping
	chainMode map[string]string
}

// ChainMode 决定同名多路由（一条 failover 链）先打哪一家。默认 failover：永远
// 从链首开始，健康就短路，只在链首失败时转下一家。RoundRobin：每次请求换一个
// 起点，让链上各家的流量大致均摊，起点之后的顺序仍是 failover。
const (
	ChainFailover   = "failover"
	ChainRoundRobin = "round_robin"
)

// NormalizeChainMode maps anything unrecognized to the default.
func NormalizeChainMode(mode string) string {
	if strings.TrimSpace(mode) == ChainRoundRobin {
		return ChainRoundRobin
	}
	return ChainFailover
}

func NewMappingStore(db *sql.DB) (*MappingStore, error) {
	s := &MappingStore{db: db, items: map[string]ModelMapping{}, chainMode: map[string]string{}}
	rows, err := db.Query(`SELECT id,client_model,provider_id,upstream_model,enabled,aliases_json,input_types_json,input_context_size,output_size,input_price,output_price,cache_read_price FROM model_mappings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m ModelMapping
		var enabled int
		var aliases, inputTypes string
		if err := rows.Scan(&m.ID, &m.ClientModel, &m.ProviderID, &m.UpstreamModel, &enabled, &aliases, &inputTypes, &m.InputContextSize, &m.OutputSize, &m.InputPrice, &m.OutputPrice, &m.CacheReadPrice); err != nil {
			return nil, err
		}
		m.Enabled = enabled == 1
		m.Aliases = decodeAliases(aliases)
		m.InputTypes = decodeInputTypes(inputTypes)
		s.items[m.ID] = m
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	modes, err := db.Query(`SELECT client_model, mode FROM model_chain_modes`)
	if err != nil {
		return nil, err
	}
	defer modes.Close()
	for modes.Next() {
		var name, mode string
		if err := modes.Scan(&name, &mode); err != nil {
			return nil, err
		}
		s.chainMode[name] = NormalizeChainMode(mode)
	}
	if err := modes.Err(); err != nil {
		return nil, err
	}
	if len(s.items) == 0 {
		_, _ = s.Save(ModelMapping{ID: "default-gpt", ClientModel: "gpt-4.1", ProviderID: "openai", UpstreamModel: "gpt-4.1", Enabled: true})
	}
	return s, nil
}
func (s *MappingStore) List() []ModelMapping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listLocked()
}

// listLocked is List without the lock, so callers already holding mu (ResolveAll)
// do not re-enter it.
func (s *MappingStore) listLocked() []ModelMapping {
	out := make([]ModelMapping, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	// 同名映射构成一条 failover 链，链内顺序必须稳定，否则每次调用解析出的顺序都可能
	// 变。ID 由创建时间派生（UI 写 mapping-<时间戳>），所以 ID 序即配置序。
	sort.Slice(out, func(i, j int) bool {
		if out[i].ClientModel != out[j].ClientModel {
			return out[i].ClientModel < out[j].ClientModel
		}
		return out[i].ID < out[j].ID
	})
	return out
}
func (s *MappingStore) Save(input ModelMapping) (ModelMapping, error) {
	if input.ID == "" || input.ClientModel == "" || input.ProviderID == "" || input.UpstreamModel == "" {
		return ModelMapping{}, errors.New("all mapping fields are required")
	}
	// Aliases are matched after trimming (request bodies arrive whitespace-free,
	// but the UI text field is not), so store the trimmed form once here and let
	// every comparison use it verbatim.
	input.Aliases = normalizeNames(input.Aliases, input.ClientModel)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkDuplicateRouteLocked(input); err != nil {
		return ModelMapping{}, err
	}
	aliases, err := json.Marshal(input.Aliases)
	if err != nil {
		return ModelMapping{}, err
	}
	// 输入模态同样在落库前规整一次，读回来的形态与 UI 勾选一致。
	// 一个都没勾视为未配置：模型默认都有 text 能力，落回 ["text"]。
	input.InputTypes = normalizeNames(input.InputTypes, "")
	if len(input.InputTypes) == 0 {
		input.InputTypes = []string{"text"}
	}
	inputTypes, err := json.Marshal(input.InputTypes)
	if err != nil {
		return ModelMapping{}, err
	}
	_, err = s.db.Exec(`INSERT INTO model_mappings(id,client_model,provider_id,upstream_model,enabled,aliases_json,input_types_json,input_context_size,output_size,input_price,output_price,cache_read_price) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET client_model=excluded.client_model,provider_id=excluded.provider_id,upstream_model=excluded.upstream_model,enabled=excluded.enabled,aliases_json=excluded.aliases_json,input_types_json=excluded.input_types_json,input_context_size=excluded.input_context_size,output_size=excluded.output_size,input_price=excluded.input_price,output_price=excluded.output_price,cache_read_price=excluded.cache_read_price`, input.ID, input.ClientModel, input.ProviderID, input.UpstreamModel, input.Enabled, string(aliases), string(inputTypes), input.InputContextSize, input.OutputSize, input.InputPrice, input.OutputPrice, input.CacheReadPrice)
	if err != nil {
		return ModelMapping{}, err
	}
	s.items[input.ID] = input
	return input, nil
}

func (s *MappingStore) DeleteByProvider(providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM model_mappings WHERE provider_id = ?`, providerID); err != nil {
		return err
	}
	for id, mapping := range s.items {
		if mapping.ProviderID == providerID {
			delete(s.items, id)
		}
	}
	return s.pruneChainModesLocked()
}

// pruneChainModesLocked drops chain-mode rows whose client model no longer has
// any mapping. They are harmless but would resurrect if the name comes back.
func (s *MappingStore) pruneChainModesLocked() error {
	for name := range s.chainMode {
		found := false
		for _, item := range s.items {
			if item.ClientModel == name {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if _, err := s.db.Exec(`DELETE FROM model_chain_modes WHERE client_model = ?`, name); err != nil {
			return err
		}
		delete(s.chainMode, name)
	}
	return nil
}

// ChainModeFor reports the starting rule for a client model's failover chain.
// Absent means the default: always start at the chain head.
func (s *MappingStore) ChainModeFor(clientModel string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if mode, ok := s.chainMode[strings.TrimSpace(clientModel)]; ok {
		return mode
	}
	return ChainFailover
}

// ChainModes returns every non-default chain mode, keyed by client model.
func (s *MappingStore) ChainModes() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.chainMode))
	for name, mode := range s.chainMode {
		out[name] = mode
	}
	return out
}

// SetChainMode stores a chain's starting rule. Setting the default removes the
// row, so a name that is never configured round-robin keeps no state.
func (s *MappingStore) SetChainMode(clientModel, mode string) error {
	name := strings.TrimSpace(clientModel)
	if name == "" {
		return errors.New("client model is required")
	}
	mode = NormalizeChainMode(mode)
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode == ChainFailover {
		if _, err := s.db.Exec(`DELETE FROM model_chain_modes WHERE client_model = ?`, name); err != nil {
			return err
		}
		delete(s.chainMode, name)
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO model_chain_modes(client_model, mode) VALUES(?,?) ON CONFLICT(client_model) DO UPDATE SET mode=excluded.mode`, name, mode); err != nil {
		return err
	}
	s.chainMode[name] = mode
	return nil
}

// Resolve finds the mapping a client model name routes to. An alias is
// interchangeable with the client model name it was declared beside, so both
// go through the same lookup.
//
// 同名多路由时用 ResolveAll：它按配置顺序返回整条链，供 proxy 依次 failover。
// Resolve 只保留给「一个名字对应一条映射」的旧调用方（UI 的 ResolveModel）。
func (s *MappingStore) Resolve(clientModel string) (ModelMapping, bool) {
	mappings := s.ResolveAll(clientModel)
	if len(mappings) == 0 {
		return ModelMapping{}, false
	}
	return mappings[0], true
}

// ResolveAll returns every enabled mapping that answers to clientModel, in
// configured order. Several providers can serve the same client model name — that
// is how one name fails over between them — so callers that route traffic need the
// whole chain, not just its head.
func (s *MappingStore) ResolveAll(clientModel string) []ModelMapping {
	name := strings.TrimSpace(clientModel)
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ModelMapping
	for _, item := range s.listLocked() {
		if !item.Enabled {
			continue
		}
		for _, candidate := range item.Names() {
			if candidate == name {
				out = append(out, item)
				break
			}
		}
	}
	return out
}

// normalizeNames trims each entry, drops blanks, duplicates, and any entry that
// merely repeats own — the client model name is already routed on its own, so
// storing it again would advertise a redundant alias.
func normalizeNames(names []string, own string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{own: {}}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// checkDuplicateRouteLocked rejects a mapping that repeats an existing route: the
// same client-side name on the same provider and upstream model would make
// failover retry the identical request and pay for it twice. Sharing a name across
// different providers is deliberate, so it is allowed.
func (s *MappingStore) checkDuplicateRouteLocked(input ModelMapping) error {
	for _, item := range s.items {
		if item.ID == input.ID {
			continue
		}
		if item.ProviderID != input.ProviderID || item.UpstreamModel != input.UpstreamModel {
			continue
		}
		taken := item.Names()
		for _, name := range input.Names() {
			for _, existing := range taken {
				if name == existing {
					return fmt.Errorf("model %q already routes to %s/%s", name, item.ProviderID, item.UpstreamModel)
				}
			}
		}
	}
	return nil
}

func decodeAliases(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil
	}
	return normalizeNames(names, "")
}

// decodeInputTypes 读 input_types_json：空串或坏数据一律当「未配置」处理，
// 与 decodeAliases 的容错口径一致。
func decodeInputTypes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var types []string
	if err := json.Unmarshal([]byte(raw), &types); err != nil {
		return nil
	}
	return normalizeNames(types, "")
}
