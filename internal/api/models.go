package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
)

// Model is one model the workspace knows (GET /v1/models).
type Model struct {
	Ref       string `json:"ref"`
	Provider  string `json:"provider"`
	Title     string `json:"title"`
	Tier      string `json:"tier"`
	Source    string `json:"source"`
	Available bool   `json:"available"`
}

// ModelProvider is a provider and whether the workspace can use it: a key of its own, or one
// Waniis manages. The key's value is never sent.
type ModelProvider struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Managed bool   `json:"managed"`
	Key     *struct {
		Last4 string `json:"last4"`
	} `json:"key"`
}

// ModelRoute is the model a role uses, and its fallbacks.
type ModelRoute struct {
	Model     string   `json:"model"`
	Fallbacks []string `json:"fallbacks"`
}

// Models is the workspace's models view.
type Models struct {
	Providers []ModelProvider       `json:"providers"`
	Models    []Model               `json:"models"`
	Routes    map[string]ModelRoute `json:"routes"`
	CanManage bool                  `json:"canManage"`
}

// Models is GET /v1/models.
func (c *Client) Models(ctx context.Context) (*Models, error) {
	var m Models
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/v1/models"}, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Default is the model prompts use when none is chosen: the agent role's.
func (m *Models) Default() string { return m.Routes["agent"].Model }

func (m *Models) provider(id string) *ModelProvider {
	for i := range m.Providers {
		if m.Providers[i].ID == id {
			return &m.Providers[i]
		}
	}
	return nil
}

// ProviderTitle is a provider's display name.
func (m *Models) ProviderTitle(id string) string {
	if p := m.provider(id); p != nil && p.Title != "" {
		return p.Title
	}
	return id
}

// Why says why a model can't be used here, or "" when it can.
func (m *Models) Why(model Model) string {
	if model.Available {
		return ""
	}
	p := m.provider(model.Provider)
	if p == nil || (p.Key == nil && !p.Managed) {
		return "no " + m.ProviderTitle(model.Provider) + " key"
	}
	return "not allowed in this workspace"
}

// Sorted lists the models: usable ones first, then by provider and title.
func (m *Models) Sorted() []Model {
	out := append([]Model(nil), m.Models...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Available != b.Available {
			return a.Available
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
	return out
}

// Matches says whether every word of query is in the model's ref, title, provider or tier.
func (m *Models) Matches(model Model, query string) bool {
	hay := strings.ToLower(strings.Join([]string{model.Ref, model.Title, model.Provider, m.ProviderTitle(model.Provider), model.Tier}, " "))
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// Find resolves what someone typed to one model: its exact ref, its id without the provider
// ("claude-sonnet-5-5" for "anthropic/claude-sonnet-5-5"), its title, or the only one that
// matches every word. It returns the candidates when several match.
func (m *Models) Find(query string) (Model, []Model, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	for _, model := range m.Models {
		ref := strings.ToLower(model.Ref)
		_, id, _ := strings.Cut(ref, "/")
		if ref == q || id == q || strings.ToLower(model.Title) == q {
			return model, nil, true
		}
	}
	var found []Model
	for _, model := range m.Sorted() {
		if m.Matches(model, q) {
			found = append(found, model)
		}
	}
	if len(found) == 1 {
		return found[0], nil, true
	}
	return Model{}, found, false
}
