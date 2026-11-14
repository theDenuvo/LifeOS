package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workspaceRegistry(t *testing.T, root string, shell bool) *Registry {
	t.Helper()
	registry := NewRegistry()
	registerWorkspaceTools(registry, workspaceConfig{root: root, shell: shell})
	return registry
}

func callTool(t *testing.T, registry *Registry, name string, arguments map[string]any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	result, err := registry.Execute(context.Background(), name, raw)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return object, nil
}

func TestWorkspaceToolsCreateEditReadAndDelete(t *testing.T) {
	root := t.TempDir()
	registry := workspaceRegistry(t, root, true)
	target := filepath.Join(root, "notes", "todo.md")

	written, err := callTool(t, registry, "write_file", map[string]any{
		"path":    target,
		"content": "# Список\n\n- первая задача\n",
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if written["status"] != "written" {
		t.Fatalf("unexpected write result: %+v", written)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("file was not created: %v", err)
	}

	read, err := callTool(t, registry, "read_file", map[string]any{"path": target})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if !strings.Contains(read["content"].(string), "первая задача") {
		t.Fatalf("read_file lost content: %+v", read)
	}
	if read["total_lines"].(float64) != 3 {
		t.Fatalf("unexpected line count: %+v", read)
	}

	edited, err := callTool(t, registry, "edit_file", map[string]any{
		"path":     target,
		"old_text": "первая задача",
		"new_text": "единственная задача",
	})
	if err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if edited["replacements"].(float64) != 1 {
		t.Fatalf("unexpected replacement count: %+v", edited)
	}
	updated, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if !strings.Contains(string(updated), "единственная задача") {
		t.Fatalf("edit was not applied: %s", updated)
	}

	found, err := callTool(t, registry, "search_files", map[string]any{
		"query": "единственная",
		"path":  root,
		"glob":  "*.md",
	})
	if err != nil {
		t.Fatalf("search_files: %v", err)
	}
	matches, _ := found["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %+v", found)
	}

	listed, err := callTool(t, registry, "list_files", map[string]any{
		"path":      root,
		"pattern":   "*.md",
		"recursive": true,
	})
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if entries, _ := listed["entries"].([]any); len(entries) != 1 {
		t.Fatalf("expected one entry, got %+v", listed)
	}

	if _, err := callTool(t, registry, "delete_path", map[string]any{"path": target}); err != nil {
		t.Fatalf("delete_path: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestWorkspaceToolsRejectPathsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	registry := workspaceRegistry(t, root, false)

	if _, err := callTool(t, registry, "write_file", map[string]any{
		"path":    "../escape.txt",
		"content": "nope",
	}); err == nil {
		t.Fatal("writing outside the configured root must fail")
	}
	if _, err := callTool(t, registry, "read_file", map[string]any{"path": "/etc/hosts"}); err == nil {
		t.Fatal("reading outside the configured root must fail")
	}
}

func TestWorkspaceToolsWithoutRootReachAnyPath(t *testing.T) {
	registry := workspaceRegistry(t, "", false)
	dir := t.TempDir()
	target := filepath.Join(dir, "any.txt")

	if _, err := callTool(t, registry, "write_file", map[string]any{"path": target, "content": "ok"}); err != nil {
		t.Fatalf("write_file with full machine access: %v", err)
	}
	if _, err := callTool(t, registry, "read_file", map[string]any{"path": target}); err != nil {
		t.Fatalf("read_file with full machine access: %v", err)
	}
}

func TestWorkspaceToolPreviewsDescribeTheChange(t *testing.T) {
	root := t.TempDir()
	registry := workspaceRegistry(t, root, true)
	target := filepath.Join(root, "app.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("prepare file: %v", err)
	}

	writePreview, err := registry.Preview("write_file", mustJSON(t, map[string]any{
		"path": target, "content": "package main\n\nfunc main() {}\n",
	}))
	if err != nil {
		t.Fatalf("preview write_file: %v", err)
	}
	if !strings.Contains(writePreview, "перезаписать") || !strings.Contains(writePreview, "app.go") {
		t.Fatalf("unexpected write preview: %s", writePreview)
	}

	editPreview, err := registry.Preview("edit_file", mustJSON(t, map[string]any{
		"path": target, "old_text": "package main", "new_text": "package app",
	}))
	if err != nil {
		t.Fatalf("preview edit_file: %v", err)
	}
	if !strings.Contains(editPreview, "Изменить файл") {
		t.Fatalf("unexpected edit preview: %s", editPreview)
	}

	commandPreview, err := registry.Preview("run_command", mustJSON(t, map[string]any{
		"command": "go test ./...",
	}))
	if err != nil {
		t.Fatalf("preview run_command: %v", err)
	}
	if !strings.Contains(commandPreview, "go test ./...") {
		t.Fatalf("unexpected command preview: %s", commandPreview)
	}
}

func TestRunCommandReturnsOutputAndExitCode(t *testing.T) {
	registry := workspaceRegistry(t, "", true)
	result, err := callTool(t, registry, "run_command", map[string]any{"command": "echo lifeos-ok"})
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	if !strings.Contains(result["output"].(string), "lifeos-ok") {
		t.Fatalf("unexpected output: %+v", result)
	}
	if result["exit_code"].(float64) != 0 {
		t.Fatalf("unexpected exit code: %+v", result)
	}

	failed, err := callTool(t, registry, "run_command", map[string]any{"command": "exit 3"})
	if err == nil && failed["exit_code"].(float64) != 3 {
		t.Fatalf("unexpected failing command result: %+v", failed)
	}
}

func TestShellToolsCanBeDisabled(t *testing.T) {
	registry := workspaceRegistry(t, t.TempDir(), false)
	for _, spec := range registry.Specs() {
		if spec.Name == "run_command" {
			t.Fatal("run_command must not be registered when shell access is disabled")
		}
	}
}

func TestWorkspaceToolsAreMutationsExceptReads(t *testing.T) {
	registry := workspaceRegistry(t, t.TempDir(), true)
	writes := map[string]bool{
		"write_file": true, "edit_file": true, "delete_path": true, "run_command": true,
	}
	for _, spec := range registry.Specs() {
		if writes[spec.Name] && registry.IsReadOnly(spec.Name) {
			t.Fatalf("%s must not be read-only", spec.Name)
		}
		if !writes[spec.Name] && !registry.IsReadOnly(spec.Name) {
			t.Fatalf("%s must be read-only", spec.Name)
		}
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

func TestFileToolsRefuseLifeOSDatabase(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "data", "lifeos.db")
	registry := NewRegistry()
	config := workspaceConfig{root: "", shell: false}
	config.protectLifeOSData(database)
	registerWorkspaceTools(registry, config)

	if _, err := callTool(t, registry, "write_file", map[string]any{"path": database, "content": "x"}); err == nil {
		t.Fatal("writing the LifeOS database must be refused")
	}
	if _, err := callTool(t, registry, "read_file", map[string]any{"path": database}); err == nil {
		t.Fatal("reading the LifeOS database must be refused")
	}
	if _, err := callTool(t, registry, "delete_path", map[string]any{"path": database}); err == nil {
		t.Fatal("deleting the LifeOS database must be refused")
	}
	if _, err := callTool(t, registry, "write_file", map[string]any{"path": database + "-wal", "content": "x"}); err == nil {
		t.Fatal("writing the WAL file must be refused")
	}
	// Other files in the same directory stay usable.
	other := filepath.Join(root, "data", "notes.md")
	if _, err := callTool(t, registry, "write_file", map[string]any{"path": other, "content": "ok"}); err != nil {
		t.Fatalf("unrelated file must stay writable: %v", err)
	}
}

func TestShellToolRefusesLifeOSDatabaseCommands(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "data", "lifeos.db")
	registry := NewRegistry()
	config := workspaceConfig{root: "", shell: true}
	config.protectLifeOSData(database)
	registerWorkspaceTools(registry, config)

	commands := []string{
		"sqlite3 data/lifeos.db \"DELETE FROM calendar_events WHERE id = 3;\"",
		"node -e \"require('fs').writeFileSync('Z:/data/lifeos.db', '')\"",
		"cp lifeos.db backup.db",
	}
	for _, command := range commands {
		if _, err := callTool(t, registry, "run_command", map[string]any{"command": command}); err == nil {
			t.Fatalf("command touching the LifeOS database must be refused: %s", command)
		}
	}

	if _, err := callTool(t, registry, "run_command", map[string]any{"command": "echo hello"}); err != nil {
		t.Fatalf("unrelated command must still run: %v", err)
	}
}
