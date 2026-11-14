package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits for the local machine tools. They exist to keep one tool result from
// consuming the whole context window, not to hide data from the model.
const (
	maxReadBytes        = 4 << 20
	maxSearchFileBytes  = 1 << 20
	maxFileContentBytes = 2 << 20
	maxCommandOutput    = 20000
	defaultReadLines    = 400
	maxReadLines        = 4000
	defaultListLimit    = 200
	maxListLimit        = 1000
	defaultSearchLimit  = 50
	maxSearchLimit      = 200
	defaultShellTimeout = 60 * time.Second
	maxShellTimeout     = 5 * time.Minute
)

// workspaceConfig configures the file and command tools.
//
// An empty root means the assistant may touch the whole machine, which is the
// product intent: the assistant works on the user's real files. Deployments can
// still pin a root directory to keep the assistant inside one tree.
type workspaceConfig struct {
	root  string
	shell bool
	// protected holds absolute paths the assistant must never read or write:
	// the app's own SQLite database. Direct access would corrupt or bypass it.
	protected []string
}

// protectLifeOSData marks the database files (and their WAL side files) as
// off-limits to the file tools.
func (c *workspaceConfig) protectLifeOSData(paths ...string) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		c.protected = append(c.protected, absolute, absolute+"-wal", absolute+"-shm", absolute+"-journal")
	}
}

// ProtectedPaths returns the absolute paths the file tools refuse to touch.
func (c workspaceConfig) ProtectedPaths() []string {
	return append([]string(nil), c.protected...)
}

// skippedWalkDirs are never traversed by recursive listings and searches.
var skippedWalkDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".svelte-kit":  true,
}

func registerWorkspaceTools(registry *Registry, config workspaceConfig) {
	registry.Register(Tool{
		Spec: assistantSpec("read_file", "Прочитать текстовый файл с номерами строк. Поддерживает offset/limit для больших файлов и двоичных данных не читает.",
			objectSchema(map[string]any{
				"path":   stringProp("Путь к файлу. Относительный путь считается от рабочего каталога LifeOS, поддерживается ~"),
				"offset": integerProp("С какой строки начать, начиная с 1. По умолчанию 1"),
				"limit":  integerProp("Сколько строк вернуть, 1-4000, по умолчанию 400"),
			}, "path")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			path, err := config.resolve(args.Path)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(path)
			if err != nil {
				return nil, fmt.Errorf("файл недоступен: %w", err)
			}
			if info.IsDir() {
				return nil, fmt.Errorf("это каталог; используй list_files")
			}
			if info.Size() > maxReadBytes {
				return nil, fmt.Errorf("файл больше %d МБ; читай его частями через run_command или offset", maxReadBytes>>20)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("не удалось прочитать файл: %w", err)
			}
			lines := splitLines(data)
			total := len(lines)
			offset := args.Offset
			if offset <= 0 {
				offset = 1
			}
			limit := args.Limit
			if limit <= 0 {
				limit = defaultReadLines
			}
			if limit > maxReadLines {
				limit = maxReadLines
			}
			start := offset - 1
			if start > total {
				start = total
			}
			end := start + limit
			if end > total {
				end = total
			}
			var b strings.Builder
			for index := start; index < end; index++ {
				fmt.Fprintf(&b, "%d\t%s\n", index+1, lines[index])
			}
			next := 0
			if end < total {
				next = end + 1
			}
			return map[string]any{
				"path":           path,
				"content":        b.String(),
				"total_lines":    total,
				"offset":         offset,
				"returned_lines": end - start,
				"next_offset":    next,
				"bytes":          info.Size(),
			}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("list_files", "Показать содержимое каталога. Возвращает имена, типы и размеры; понимает glob-шаблоны.",
			objectSchema(map[string]any{
				"path":      stringProp("Каталог. По умолчанию рабочий каталог LifeOS"),
				"pattern":   stringProp("Glob-шаблон по имени, например *.go или *session*"),
				"recursive": booleanProp("Обходить вложенные каталоги (по умолчанию нет)"),
				"limit":     integerProp("Сколько записей вернуть, 1-1000, по умолчанию 200"),
			})),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Path      string `json:"path"`
				Pattern   string `json:"pattern"`
				Recursive bool   `json:"recursive"`
				Limit     int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			root, err := config.resolve(args.Path)
			if err != nil {
				if strings.TrimSpace(args.Path) != "" {
					return nil, err
				}
				root, err = config.workingDir()
				if err != nil {
					return nil, err
				}
			}
			info, err := os.Stat(root)
			if err != nil {
				return nil, fmt.Errorf("каталог недоступен: %w", err)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("это файл; используй read_file")
			}
			limit := args.Limit
			if limit <= 0 {
				limit = defaultListLimit
			}
			if limit > maxListLimit {
				limit = maxListLimit
			}
			pattern := strings.TrimSpace(args.Pattern)
			entries := make([]map[string]any, 0, min(limit, 64))
			truncated := false
			appendEntry := func(path string, entry fs.DirEntry) bool {
				name := entry.Name()
				if pattern != "" {
					if ok, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(name)); !ok {
						return true
					}
				}
				if len(entries) >= limit {
					truncated = true
					return false
				}
				item := map[string]any{"path": path, "name": name}
				if entry.IsDir() {
					item["type"] = "dir"
				} else {
					item["type"] = "file"
					if info, err := entry.Info(); err == nil {
						item["size"] = info.Size()
					}
				}
				entries = append(entries, item)
				return true
			}
			if args.Recursive {
				err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					if entry.IsDir() {
						if path != root && skippedWalkDirs[entry.Name()] {
							return fs.SkipDir
						}
						return nil
					}
					if !appendEntry(path, entry) {
						return fs.SkipAll
					}
					return nil
				})
			} else {
				var dir []os.DirEntry
				dir, err = os.ReadDir(root)
				if err == nil {
					for _, entry := range dir {
						if !appendEntry(filepath.Join(root, entry.Name()), entry) {
							break
						}
					}
				}
			}
			if err != nil {
				return nil, fmt.Errorf("не удалось прочитать каталог: %w", err)
			}
			sort.Slice(entries, func(i, j int) bool {
				return fmt.Sprint(entries[i]["path"]) < fmt.Sprint(entries[j]["path"])
			})
			return map[string]any{
				"path":      root,
				"entries":   entries,
				"truncated": truncated,
			}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("search_files", "Найти текст в файлах каталога. Возвращает файл, номер строки и саму строку.",
			objectSchema(map[string]any{
				"query":          stringProp("Что искать"),
				"path":           stringProp("Каталог поиска. По умолчанию рабочий каталог LifeOS"),
				"glob":           stringProp("Фильтр по имени файла, например *.go или *.md"),
				"case_sensitive": booleanProp("Учитывать регистр (по умолчанию нет)"),
				"limit":          integerProp("Сколько совпадений вернуть, 1-200, по умолчанию 50"),
			}, "query")),
		ReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Query         string `json:"query"`
				Path          string `json:"path"`
				Glob          string `json:"glob"`
				CaseSensitive bool   `json:"case_sensitive"`
				Limit         int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			query := strings.TrimSpace(args.Query)
			if query == "" {
				return nil, errors.New("query обязателен")
			}
			root, err := config.resolve(args.Path)
			if err != nil {
				if strings.TrimSpace(args.Path) != "" {
					return nil, err
				}
				root, err = config.workingDir()
				if err != nil {
					return nil, err
				}
			}
			limit := args.Limit
			if limit <= 0 {
				limit = defaultSearchLimit
			}
			if limit > maxSearchLimit {
				limit = maxSearchLimit
			}
			needle := query
			if !args.CaseSensitive {
				needle = strings.ToLower(needle)
			}
			matches := make([]map[string]any, 0, limit)
			truncated := false
			err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				if entry.IsDir() {
					if path != root && skippedWalkDirs[entry.Name()] {
						return fs.SkipDir
					}
					return nil
				}
				if pattern := strings.TrimSpace(args.Glob); pattern != "" {
					if ok, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(entry.Name())); !ok {
						return nil
					}
				}
				info, statErr := entry.Info()
				if statErr != nil || info.Size() > maxSearchFileBytes {
					return nil
				}
				data, readErr := os.ReadFile(path)
				if readErr != nil || bytes.IndexByte(data, 0) >= 0 {
					return nil
				}
				for index, line := range splitLines(data) {
					haystack := line
					if !args.CaseSensitive {
						haystack = strings.ToLower(line)
					}
					if !strings.Contains(haystack, needle) {
						continue
					}
					if len(matches) >= limit {
						truncated = true
						return fs.SkipAll
					}
					matches = append(matches, map[string]any{
						"path": path, "line": index + 1, "text": truncateRunesText(line, 240),
					})
					if len(matches) >= limit {
						truncated = true
						return fs.SkipAll
					}
				}
				return nil
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				return nil, fmt.Errorf("поиск не удался: %w", err)
			}
			return map[string]any{
				"path":      root,
				"matches":   matches,
				"truncated": truncated,
			}, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("write_file", "Создать файл или полностью перезаписать его содержимым. Если файла ещё нет — это нормально, он будет создан. Для точечных правок используй edit_file.",
			objectSchema(map[string]any{
				"path":        stringProp("Путь к файлу; недостающие каталоги создаются"),
				"content":     stringProp("Полное новое содержимое файла"),
				"create_dirs": booleanProp("Создавать недостающие каталоги (по умолчанию да)"),
			}, "path", "content")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Path       string `json:"path"`
				Content    string `json:"content"`
				CreateDirs *bool  `json:"create_dirs"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			path, err := config.resolve(args.Path)
			if err != nil {
				return nil, err
			}
			createDirs := args.CreateDirs == nil || *args.CreateDirs
			if createDirs {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return nil, fmt.Errorf("не удалось создать каталог: %w", err)
				}
			}
			mode := os.FileMode(0o644)
			if info, err := os.Stat(path); err == nil {
				if info.IsDir() {
					return nil, fmt.Errorf("это каталог, а не файл")
				}
				mode = info.Mode().Perm()
			}
			if err := os.WriteFile(path, []byte(args.Content), mode); err != nil {
				return nil, fmt.Errorf("не удалось записать файл: %w", err)
			}
			return map[string]any{
				"path":   path,
				"bytes":  len(args.Content),
				"lines":  len(splitLines([]byte(args.Content))),
				"status": "written",
			}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			path, err := config.resolve(args.Path)
			if err != nil {
				return "", err
			}
			action := "создать"
			if _, statErr := os.Stat(path); statErr == nil {
				action = "перезаписать"
			}
			return fmt.Sprintf("Записать файл «%s» (%s): %d строк, %d байт.",
				path, action, len(splitLines([]byte(args.Content))), len(args.Content)), nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("edit_file", "Точечно изменить файл: заменить точный фрагмент old_text на new_text.",
			objectSchema(map[string]any{
				"path":        stringProp("Путь к существующему файлу"),
				"old_text":    stringProp("Точный фрагмент, который нужно найти (включая отступы)"),
				"new_text":    stringProp("Чем заменить; пустая строка удаляет фрагмент"),
				"replace_all": booleanProp("Заменить все вхождения, а не только единственное"),
			}, "path", "old_text", "new_text")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Path       string `json:"path"`
				OldText    string `json:"old_text"`
				NewText    string `json:"new_text"`
				ReplaceAll bool   `json:"replace_all"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if args.OldText == "" {
				return nil, errors.New("old_text не может быть пустым; используй write_file")
			}
			path, err := config.resolve(args.Path)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(path)
			if err != nil {
				return nil, fmt.Errorf("файл недоступен: %w", err)
			}
			if info.Size() > maxFileContentBytes {
				return nil, fmt.Errorf("файл больше %d МБ", maxFileContentBytes>>20)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("не удалось прочитать файл: %w", err)
			}
			count := strings.Count(string(data), args.OldText)
			if count == 0 {
				return nil, fmt.Errorf("фрагмент old_text не найден в файле")
			}
			if count > 1 && !args.ReplaceAll {
				return nil, fmt.Errorf("фрагмент встречается %d раз; уточни old_text или включи replace_all", count)
			}
			updated := strings.Replace(string(data), args.OldText, args.NewText, 1)
			replaced := 1
			if args.ReplaceAll {
				updated = strings.ReplaceAll(string(data), args.OldText, args.NewText)
				replaced = count
			}
			if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
				return nil, fmt.Errorf("не удалось записать файл: %w", err)
			}
			return map[string]any{
				"path":         path,
				"replacements": replaced,
				"bytes":        len(updated),
				"status":       "edited",
			}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Path       string `json:"path"`
				OldText    string `json:"old_text"`
				NewText    string `json:"new_text"`
				ReplaceAll bool   `json:"replace_all"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			path, err := config.resolve(args.Path)
			if err != nil {
				return "", err
			}
			summary := fmt.Sprintf("Изменить файл «%s»: заменить фрагмент (%d симв. → %d симв.).",
				path, utf8.RuneCountInString(args.OldText), utf8.RuneCountInString(args.NewText))
			if data, readErr := os.ReadFile(path); readErr == nil {
				count := strings.Count(string(data), args.OldText)
				if count == 0 {
					summary += " Фрагмент в файле не найден."
				} else if count > 1 && !args.ReplaceAll {
					summary += fmt.Sprintf(" Фрагмент встречается %d раза — уточните old_text.", count)
				} else if count > 1 {
					summary += fmt.Sprintf(" Будет заменено вхождений: %d.", count)
				}
			}
			return summary, nil
		},
	})

	registry.Register(Tool{
		Spec: assistantSpec("delete_path", "Удалить файл или каталог. Каталог удаляется только с флагом recursive.",
			objectSchema(map[string]any{
				"path":      stringProp("Что удалить"),
				"recursive": booleanProp("Удалить каталог со всем содержимым"),
			}, "path")),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Path      string `json:"path"`
				Recursive bool   `json:"recursive"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			path, err := config.resolve(args.Path)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(path)
			if err != nil {
				return nil, fmt.Errorf("путь недоступен: %w", err)
			}
			if info.IsDir() {
				if !args.Recursive {
					return nil, fmt.Errorf("это каталог; поставь recursive=true, чтобы удалить со всем содержимым")
				}
				if err := os.RemoveAll(path); err != nil {
					return nil, fmt.Errorf("не удалось удалить каталог: %w", err)
				}
				return map[string]any{"path": path, "status": "deleted", "type": "dir"}, nil
			}
			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("не удалось удалить файл: %w", err)
			}
			return map[string]any{"path": path, "status": "deleted", "type": "file", "bytes": info.Size()}, nil
		},
		Preview: func(raw json.RawMessage) (string, error) {
			var args struct {
				Path      string `json:"path"`
				Recursive bool   `json:"recursive"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			path, err := config.resolve(args.Path)
			if err != nil {
				return "", err
			}
			info, statErr := os.Stat(path)
			if statErr != nil {
				return fmt.Sprintf("Удалить «%s»: путь не найден.", path), nil
			}
			if info.IsDir() {
				if !args.Recursive {
					return fmt.Sprintf("Удалить каталог «%s»: нужен флаг recursive, иначе удаление не выполнится.", path), nil
				}
				return fmt.Sprintf("Удалить каталог «%s» со всем содержимым.", path), nil
			}
			return fmt.Sprintf("Удалить файл «%s» (%d байт).", path, info.Size()), nil
		},
	})

	if config.shell {
		registry.Register(Tool{
			Spec: assistantSpec("run_command", "Выполнить команду оболочки на машине пользователя и вернуть вывод, код возврата и длительность.",
				objectSchema(map[string]any{
					"command":         stringProp("Команда целиком, как в терминале"),
					"cwd":             stringProp("Рабочий каталог. По умолчанию рабочий каталог LifeOS"),
					"timeout_seconds": integerProp("Таймаут в секундах, 1-300, по умолчанию 60"),
				}, "command")),
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					Command        string `json:"command"`
					Cwd            string `json:"cwd"`
					TimeoutSeconds int    `json:"timeout_seconds"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return nil, err
				}
				command := strings.TrimSpace(args.Command)
				if command == "" {
					return nil, errors.New("command обязателен")
				}
				if hit := config.protectedMention(command); hit != "" {
					return nil, fmt.Errorf("команда обращается к %s — внутренние данные LifeOS; используй инструменты задач и календаря", hit)
				}
				dir, err := config.resolve(args.Cwd)
				if err != nil {
					if strings.TrimSpace(args.Cwd) != "" {
						return nil, err
					}
					dir, err = config.workingDir()
					if err != nil {
						return nil, err
					}
				}
				if info, err := os.Stat(dir); err != nil || !info.IsDir() {
					return nil, fmt.Errorf("рабочий каталог недоступен: %s", dir)
				}
				timeout := time.Duration(args.TimeoutSeconds) * time.Second
				if timeout <= 0 {
					timeout = defaultShellTimeout
				}
				if timeout > maxShellTimeout {
					timeout = maxShellTimeout
				}
				runCtx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()

				name, shellArgs := shellCommand(command)
				cmd := exec.CommandContext(runCtx, name, shellArgs...)
				cmd.Dir = dir
				var output bytes.Buffer
				cmd.Stdout = &limitedWriter{buffer: &output, limit: maxCommandOutput}
				cmd.Stderr = &limitedWriter{buffer: &output, limit: maxCommandOutput}
				started := time.Now()
				runErr := cmd.Run()
				elapsed := time.Since(started)

				result := map[string]any{
					"command":     command,
					"cwd":         dir,
					"output":      output.String(),
					"truncated":   output.Len() >= maxCommandOutput,
					"duration_ms": elapsed.Milliseconds(),
					"exit_code":   0,
				}
				if runErr != nil {
					var exitErr *exec.ExitError
					switch {
					case errors.Is(runCtx.Err(), context.DeadlineExceeded):
						result["timed_out"] = true
						result["error"] = fmt.Sprintf("команда прервана по таймауту %s", timeout)
					case errors.As(runErr, &exitErr):
						result["exit_code"] = exitErr.ExitCode()
					default:
						result["error"] = runErr.Error()
						result["exit_code"] = -1
					}
				}
				return result, nil
			},
			Preview: func(raw json.RawMessage) (string, error) {
				var args struct {
					Command string `json:"command"`
					Cwd     string `json:"cwd"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return "", err
				}
				dir, err := config.resolve(args.Cwd)
				if err != nil {
					dir = args.Cwd
					if strings.TrimSpace(dir) == "" {
						dir, _ = config.workingDir()
					}
				}
				return fmt.Sprintf("Выполнить команду в «%s»: %s", dir, truncateRunesText(args.Command, 300)), nil
			},
		})
	}
}

// resolve turns a tool path into an absolute path and enforces the optional
// workspace root.
func (c workspaceConfig) resolve(path string) (string, error) {
	raw := strings.TrimSpace(path)
	if raw == "" {
		if c.root != "" {
			return filepath.Abs(c.root)
		}
		return "", errors.New("path обязателен")
	}
	expanded, err := expandHome(raw)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		base := c.root
		if base == "" {
			base, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		expanded = filepath.Join(base, expanded)
	}
	absolute, err := filepath.Abs(filepath.Clean(expanded))
	if err != nil {
		return "", err
	}
	if c.isProtected(absolute) {
		return "", fmt.Errorf("%s — внутренние данные LifeOS; работай с ними через инструменты задач и календаря", filepath.Base(absolute))
	}
	if c.root == "" {
		return absolute, nil
	}
	root, err := filepath.Abs(c.root)
	if err != nil {
		return "", err
	}
	resolvedRoot := evalExisting(root)
	resolvedPath := evalExisting(absolute)
	if !withinRoot(resolvedRoot, resolvedPath) {
		return "", fmt.Errorf("путь вне разрешённого каталога %s", root)
	}
	return absolute, nil
}

func (c workspaceConfig) workingDir() (string, error) {
	if c.root != "" {
		return filepath.Abs(c.root)
	}
	return os.Getwd()
}

// evalExisting resolves symlinks for the deepest existing ancestor so a symlink
// cannot be used to escape a restricted root.
func evalExisting(path string) string {
	current := path
	suffix := ""
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, suffix)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = filepath.Join(filepath.Base(current), suffix)
		current = parent
	}
}

// protectedMention reports a protected file named in a shell command, so the
// assistant cannot bypass the file-tool guard with sqlite/node one-liners.
func (c workspaceConfig) protectedMention(command string) string {
	lower := strings.ToLower(command)
	for _, item := range c.protected {
		name := strings.ToLower(filepath.Base(item))
		if name == "" || strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") || strings.HasSuffix(name, "-journal") {
			continue
		}
		if strings.Contains(lower, name) {
			return name
		}
	}
	return ""
}

func (c workspaceConfig) isProtected(path string) bool {
	for _, item := range c.protected {
		if strings.EqualFold(path, item) || path == evalExisting(item) {
			return true
		}
	}
	return false
}

func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..")
}

func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), string(filepath.Separator))), nil
}

func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/C", command}
	}
	return "/bin/sh", []string{"-c", command}
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func truncateRunesText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func booleanProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

// limitedWriter keeps the first limit bytes and silently drops the rest so a
// chatty command cannot exhaust memory.
type limitedWriter struct {
	buffer *bytes.Buffer
	limit  int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = w.buffer.Write(p[:remaining])
		return len(p), nil
	}
	_, _ = w.buffer.Write(p)
	return len(p), nil
}

var _ io.Writer = (*limitedWriter)(nil)
