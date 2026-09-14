package agent

import (
	"errors"
	"sort"
	"sync"
)

type Preset struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Model        string   `json:"model"`
	SystemPrompt string   `json:"systemPrompt"`
	Tools        []string `json:"tools"`
}
type Store struct {
	mu    sync.RWMutex
	items map[string]Preset
}

func NewStore() *Store {
	return &Store{items: map[string]Preset{"code-review": {ID: "code-review", Name: "Code Reviewer", Description: "Review code changes with a focused checklist.", Model: "gpt-4.1", Tools: []string{"filesystem", "terminal"}}}}
}
func (s *Store) List() []Preset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Preset, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (s *Store) Save(input Preset) (Preset, error) {
	if input.ID == "" || input.Name == "" || input.Model == "" {
		return Preset{}, errors.New("preset id, name and model are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[input.ID] = input
	return input, nil
}
