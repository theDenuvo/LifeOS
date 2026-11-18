package tools

import (
	"context"
	"errors"
	"testing"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
)

// newResearchRegistry builds a registry with the research tools attached and
// nothing else, which is enough to check their contract.
func newResearchRegistry() *Registry {
	registry := NewRegistry()
	RegisterResearchTools(registry, appresearch.NewService(nil, nil))
	return registry
}

func TestResearchToolsAreRegistered(t *testing.T) {
	registry := newResearchRegistry()
	for _, name := range []string{
		"web_search", "read_web_page", "research_topic", "list_research", "get_research",
		"list_external_sources", "add_external_source", "update_external_source",
		"remove_external_source", "call_external_source", "test_external_source",
		"list_watches", "create_watch", "check_watch", "update_watch", "delete_watch",
	} {
		if !registry.Has(name) {
			t.Fatalf("tool %q is not registered", name)
		}
	}
}

func TestResearchReadOnlyBoundary(t *testing.T) {
	registry := newResearchRegistry()
	readOnly := []string{"web_search", "read_web_page", "list_research", "get_research", "list_external_sources", "list_watches"}
	for _, name := range readOnly {
		if !registry.IsReadOnly(name) {
			t.Fatalf("%q must be read-only: it only reads", name)
		}
	}
	// Saving a report is a reversible write that applies immediately, like
	// learning from a document.
	for _, name := range []string{"research_topic", "create_watch", "check_watch"} {
		if registry.IsReadOnly(name) {
			t.Fatalf("%q stores something, so it is not read-only", name)
		}
		if !registry.IsDirect(name) {
			t.Fatalf("%q is reversible and must apply without a confirmation", name)
		}
	}
	// Removing a source, an outbound call and a source check are decisions for
	// the user: checking a script source runs the user's command, and the
	// command's effects are not something LifeOS can promise.
	for _, name := range []string{"remove_external_source", "call_external_source", "add_external_source", "delete_watch", "test_external_source"} {
		if registry.IsDirect(name) {
			t.Fatalf("%q must wait for confirmation", name)
		}
		if registry.IsReadOnly(name) {
			t.Fatalf("%q is not read-only", name)
		}
	}
}

func TestAutomationValidatorRefusesPermissionTools(t *testing.T) {
	registry := newResearchRegistry()
	validator := AutomationStepValidator(registry)

	for _, name := range []string{"remove_external_source", "call_external_source", "add_external_source", "update_external_source", "test_external_source"} {
		allowed, reason := validator.AllowedForAutomation(name)
		if allowed {
			t.Fatalf("%q must not run unattended", name)
		}
		if reason == "" {
			t.Fatalf("%q needs a reason when refused", name)
		}
	}

	// Research itself is fine: it reads the web and stores a report.
	for _, name := range []string{"web_search", "read_web_page", "research_topic", "list_watches", "check_watch"} {
		allowed, reason := validator.AllowedForAutomation(name)
		if !allowed {
			t.Fatalf("%q should be allowed in an automation: %s", name, reason)
		}
	}
}

func TestResearchToolErrorsAreReadable(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{appresearch.ErrNotFound, "исследование не найдено"},
		{appresearch.ErrSourceNotFound, "внешний источник не найден"},
		{appresearch.ErrNoProvider, "поиск в интернете не настроен"},
		{appresearch.ErrVersionConflict, "актуальной версией"},
	}
	for _, item := range cases {
		mapped := researchToolError(item.err)
		if mapped == nil || !containsText(mapped.Error(), item.want) {
			t.Fatalf("researchToolError(%v) = %v, want text %q", item.err, mapped, item.want)
		}
	}
	if !errors.Is(researchToolError(appresearch.ErrSourceReadOnly), appresearch.ErrSourceReadOnly) {
		t.Fatal("the wrapped read-only error must stay inspectable")
	}
}

func TestAddExternalSourceRefusesScriptsFromTheAssistant(t *testing.T) {
	registry := newResearchRegistry()
	_, err := registry.Execute(context.Background(), "add_external_source", []byte(`{
		"name": "Скрипт", "kind": "script", "endpoint": "hostname"
	}`))
	if err == nil {
		t.Fatal("the assistant must not register a script source")
	}
	if !errors.Is(researchToolError(err), appresearch.ErrScriptNeedsUser) {
		t.Fatalf("expected ErrScriptNeedsUser, got %v", err)
	}
}

func containsText(value, want string) bool {
	if len(want) > len(value) {
		return false
	}
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
