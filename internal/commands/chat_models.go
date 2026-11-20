package commands

import (
	"fmt"
	"strings"

	"github.com/elmkarami/waniis-cli/internal/api"
	"github.com/elmkarami/waniis-cli/internal/chat"
)

// modelTabs are the picker's tabs: every model, the ones this workspace can use, the ones it
// can't (no key, or not allowed), and a search over all of them.
var modelTabs = []chat.PickTab{
	{Name: "All"},
	{Name: "Configured", Show: func(it chat.PickItem) bool { return !it.Disabled }},
	{Name: "Not configured", Show: func(it chat.PickItem) bool { return it.Disabled }},
	{Name: "Search", Search: true},
}

// modelItems are the models as picker rows, the one in use marked.
func modelItems(m *api.Models, current string) []chat.PickItem {
	var items []chat.PickItem
	for _, model := range m.Sorted() {
		detail := model.Ref
		if model.Tier != "" {
			detail += "  " + model.Tier
		}
		if model.Ref == m.Default() {
			detail += "  (workspace default)"
		}
		items = append(items, chat.PickItem{
			ID: model.Ref, Label: model.Title, Detail: detail,
			Note: m.Why(model), Disabled: !model.Available, Current: model.Ref == current,
		})
	}
	return items
}

// modelInUse is what the next prompts use: the one chosen in this chat, or the workspace's.
func (s *chatSession) modelInUse(m *api.Models) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.model != "" {
		return s.model
	}
	return m.Default()
}

func (s *chatSession) setModel(ref, say string) {
	s.mu.Lock()
	s.model = ref
	s.refreshPrompt()
	s.mu.Unlock()
	s.dim(say)
}

// cannotUse explains why a model can't be chosen and who can fix it.
func (s *chatSession) cannotUse(m *api.Models, model api.Model) {
	where := s.rt.Base + "/settings/models"
	if m.CanManage {
		s.say(fmt.Sprintf("%s can't be used yet: %s. Add it in Settings › Models: %s", model.Title, m.Why(model), where))
		return
	}
	s.say(fmt.Sprintf("%s can't be used yet: %s. A workspace owner or admin can add it in Settings › Models.", model.Title, m.Why(model)))
}

// chooseModel is /model: no argument opens the picker (a list without a terminal), "default"
// goes back to the workspace's model, and a name chooses one.
func (s *chatSession) chooseModel(arg string) {
	m, err := s.rt.Client.Models(s.ctx)
	if err != nil {
		s.report(err, "")
		return
	}
	if locked, why := m.Locked(); locked {
		// The router decides; a model chosen here would be refused when the prompt is sent.
		s.setModel("", why)
		return
	}
	switch strings.ToLower(arg) {
	case "":
		s.pickModel(m)
		return
	case "default", "reset":
		s.setModel("", "Next prompts use the workspace's model, "+titleOf(m, m.Default())+".")
		return
	}
	model, candidates, ok := m.Find(arg)
	if !ok {
		if len(candidates) == 0 {
			s.say("No model matches " + arg + ". /model lists them.")
			return
		}
		s.say("Several models match " + arg + ":")
		for i, c := range candidates {
			if i == 6 {
				s.dim(fmt.Sprintf("  … and %d more. /model to search them.", len(candidates)-i))
				break
			}
			s.say("  " + c.Title + "  " + s.sty.Dim(c.Ref))
		}
		return
	}
	if !model.Available {
		s.cannotUse(m, model)
		return
	}
	s.setModel(model.Ref, "Next prompts use "+model.Title+" ("+model.Ref+").")
}

func titleOf(m *api.Models, ref string) string {
	for _, model := range m.Models {
		if model.Ref == ref {
			return model.Title
		}
	}
	if ref == "" {
		return "its default"
	}
	return ref
}

func (s *chatSession) pickModel(m *api.Models) {
	current := s.modelInUse(m)
	items := modelItems(m, current)
	if len(items) == 0 {
		s.say("This workspace has no models yet. Add a provider key in Settings › Models: " + s.rt.Base + "/settings/models")
		return
	}
	if !s.con.Raw {
		// No terminal to draw a picker in: list them, usable ones first.
		for _, it := range items {
			mark := "  "
			if it.Current {
				mark = "● "
			}
			line := mark + it.Label + "  " + s.sty.Dim(it.Detail)
			if it.Note != "" {
				line = s.sty.Dim(mark + it.Label + "  " + it.Detail + "  · " + it.Note)
			}
			s.say(line)
		}
		s.dim("/model <name> chooses one; /model default goes back to the workspace's.")
		return
	}
	sel := 0
	for i, it := range items {
		if it.Current {
			sel = i
		}
	}
	s.con.OpenPicker(&chat.Picker{
		Title: "Model",
		Tabs:  modelTabs,
		Items: items,
		Sel:   sel,
		Match: func(it chat.PickItem, q string) bool {
			for _, model := range m.Models {
				if model.Ref == it.ID {
					return m.Matches(model, q)
				}
			}
			return false
		},
		Done: func(it *chat.PickItem) {
			if it == nil {
				return
			}
			for _, model := range m.Models {
				if model.Ref != it.ID {
					continue
				}
				if !model.Available {
					s.cannotUse(m, model)
					return
				}
				s.setModel(model.Ref, "Next prompts use "+model.Title+" ("+model.Ref+").")
				return
			}
		},
	})
}
