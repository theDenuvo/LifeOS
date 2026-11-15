package completion

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	appevents "github.com/theDenuvo/LifeOS/internal/application/events"
	"github.com/theDenuvo/LifeOS/internal/domain/event"
	"github.com/theDenuvo/LifeOS/internal/domain/task"
)

// DocumentHit is one document whose text matched a probe.
type DocumentHit struct {
	ID        int64
	Filename  string
	Excerpt   string
	UpdatedAt time.Time
}

// DocumentSearcher finds documents mentioning the probes.
type DocumentSearcher interface {
	SearchText(ctx context.Context, probes []string, since time.Time, limit int) ([]DocumentHit, error)
}

// DocumentSource looks for the expected result inside uploaded documents.
//
// Documents are the richest text LifeOS really owns: they are uploaded by the user
// and already extracted to Markdown, so finding the expected result there is
// evidence the user produced it.
type DocumentSource struct {
	Docs DocumentSearcher
}

// Name implements Source.
func (s *DocumentSource) Name() string { return "documents" }

// Status implements Source.
func (s *DocumentSource) Status(ctx context.Context) SourceStatus {
	if s == nil || s.Docs == nil {
		return SourceStatus{Name: s.Name(), Available: false, Reason: "хранилище документов не подключено"}
	}
	return SourceStatus{Name: s.Name(), Available: true}
}

// Evidence implements Source.
func (s *DocumentSource) Evidence(ctx context.Context, value *task.Task, expected string) ([]Evidence, error) {
	if s == nil || s.Docs == nil {
		return nil, nil
	}
	probes := probesFor(expected, value.Title)
	if len(probes) == 0 {
		return nil, nil
	}
	since := value.CreatedAt.Add(-time.Minute)
	hits, err := s.Docs.SearchText(ctx, probes, since, 10)
	if err != nil {
		return nil, err
	}
	out := make([]Evidence, 0, len(hits))
	for _, hit := range hits {
		reference := fmt.Sprintf("документ #%d «%s»", hit.ID, hit.Filename)
		// A document that says the result is not there must not be read as proof
		// that it is.
		if marker := negationMarker(hit.Excerpt); marker != "" {
			out = append(out, Evidence{
				Source:     s.Name(),
				Reference:  reference,
				Snippet:    hit.Excerpt,
				At:         hit.UpdatedAt,
				Confidence: 0.85,
				Against:    true,
			})
			continue
		}
		confidence := 0.6
		if expected != "" {
			// A document that literally contains the stated result is much stronger
			// than one that merely mentions the same words.
			confidence = 0.9
		}
		out = append(out, Evidence{
			Source:     s.Name(),
			Reference:  reference,
			Snippet:    hit.Excerpt,
			At:         hit.UpdatedAt,
			Confidence: confidence,
		})
	}
	return out, nil
}

// FileHit is one file on the machine that matched.
type FileHit struct {
	Path      string
	Excerpt   string
	UpdatedAt time.Time
}

// FilesSource looks for the expected result in the user's own files.
//
// The scan is bounded on purpose: a full disk walk would turn one verification into
// minutes of I/O. It looks at recently modified text files under the tools root and
// treats a file as evidence only when it is newer than the task, so old output does
// not confirm new work.
type FilesSource struct {
	Root     string
	MaxFiles int
	// Skip are directory names that never contain the user's work output.
	Skip []string
}

// Name implements Source.
func (s *FilesSource) Name() string { return "files" }

// Status implements Source.
func (s *FilesSource) Status(ctx context.Context) SourceStatus {
	if s == nil || s.Root == "" {
		return SourceStatus{Name: s.Name(), Available: false, Reason: "каталог не задан"}
	}
	if _, err := os.Stat(s.Root); err != nil {
		return SourceStatus{Name: s.Name(), Available: false, Reason: "каталог недоступен"}
	}
	return SourceStatus{Name: s.Name(), Available: true}
}

// Evidence implements Source.
func (s *FilesSource) Evidence(ctx context.Context, value *task.Task, expected string) ([]Evidence, error) {
	if s == nil || s.Root == "" {
		return nil, nil
	}
	words := keywords(expected)
	if len(words) == 0 {
		words = keywords(value.Title)
	}
	if len(words) == 0 {
		return nil, nil
	}
	since := value.CreatedAt.Add(-time.Minute)
	hits, err := s.scan(ctx, words, since)
	if err != nil {
		return nil, err
	}
	out := make([]Evidence, 0, len(hits))
	for _, hit := range hits {
		out = append(out, Evidence{
			Source:     s.Name(),
			Reference:  hit.Path,
			Snippet:    hit.Excerpt,
			At:         hit.UpdatedAt,
			Confidence: 0.75,
		})
	}
	return out, nil
}

func (s *FilesSource) scan(ctx context.Context, words []string, since time.Time) ([]FileHit, error) {
	maxFiles := s.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 2000
	}
	skip := s.Skip
	if len(skip) == 0 {
		skip = []string{".git", "node_modules", "dist", "build", ".cache", "vendor", "__pycache__"}
	}
	skipped := make(map[string]bool, len(skip))
	for _, name := range skip {
		skipped[name] = true
	}

	hits := make([]FileHit, 0, 8)
	inspected := 0
	err := filepath.WalkDir(s.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if skipped[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		inspected++
		if inspected > maxFiles || len(hits) >= 5 {
			return fs.SkipAll
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > 256*1024 || info.ModTime().Before(since) {
			return nil
		}
		// The database, its write-ahead log and the logs are LifeOS state, not
		// evidence about the user's work.
		if isProtectedName(entry.Name()) {
			return nil
		}
		if matchesName(entry.Name(), words) {
			hits = append(hits, FileHit{Path: path, Excerpt: "имя файла совпадает", UpdatedAt: info.ModTime().UTC()})
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(content)
		if !containsAllWords(text, words) {
			return nil
		}
		hits = append(hits, FileHit{
			Path:      path,
			Excerpt:   excerptAround(text, words[0], 120),
			UpdatedAt: info.ModTime().UTC(),
		})
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	return hits, nil
}

// CalendarSource checks whether an appointment matching the task already happened.
//
// A past meeting is real evidence that the work took place, but weak evidence that
// its result exists, so it only supports a conclusion instead of making one.
type CalendarSource struct {
	Events EventLister
}

// EventLister reads calendar events.
type EventLister interface {
	ListRange(ctx context.Context, from, to time.Time, calendars []event.Calendar) ([]appevents.EventWithVersion, error)
}

// Name implements Source.
func (s *CalendarSource) Name() string { return "calendar" }

// Status implements Source.
func (s *CalendarSource) Status(ctx context.Context) SourceStatus {
	if s == nil || s.Events == nil {
		return SourceStatus{Name: s.Name(), Available: false, Reason: "календарь не подключён"}
	}
	return SourceStatus{Name: s.Name(), Available: true}
}

// Evidence implements Source.
func (s *CalendarSource) Evidence(ctx context.Context, value *task.Task, expected string) ([]Evidence, error) {
	if s == nil || s.Events == nil {
		return nil, nil
	}
	words := keywords(value.Title)
	if len(words) == 0 {
		return nil, nil
	}
	from := value.CreatedAt.AddDate(0, 0, -7)
	found, err := s.Events.ListRange(ctx, from.UTC(), value.CreatedAt.AddDate(0, 0, 90).UTC(), nil)
	if err != nil {
		return nil, err
	}
	out := make([]Evidence, 0, 2)
	for _, item := range found {
		if !sharesWords(item.Event.Title, words) {
			continue
		}
		out = append(out, Evidence{
			Source:     s.Name(),
			Reference:  "событие «" + item.Event.Title + "»",
			At:         item.Event.StartsAt,
			Confidence: 0.4,
		})
	}
	return out, nil
}

// UnavailableSource documents a source that LifeOS cannot consult, so the report
// never implies it was checked.
type UnavailableSource struct {
	SourceName string
	Reason     string
}

// Name implements Source.
func (s UnavailableSource) Name() string { return s.SourceName }

// Status implements Source.
func (s UnavailableSource) Status(ctx context.Context) SourceStatus {
	return SourceStatus{Name: s.SourceName, Available: false, Reason: s.Reason}
}

// Evidence implements Source.
func (s UnavailableSource) Evidence(ctx context.Context, value *task.Task, expected string) ([]Evidence, error) {
	return nil, nil
}

// NewGitHubSource reports the real state: LifeOS has no GitHub integration
// configured, so it is not consulted.
func NewGitHubSource() Source {
	return UnavailableSource{
		SourceName: "github",
		Reason:     "интеграция с GitHub не настроена: у LifeOS нет токена и клиента API",
	}
}

// NewMessengerSource reports that no chat integration exists.
func NewMessengerSource() Source {
	return UnavailableSource{
		SourceName: "messages",
		Reason:     "переписки не подключены: у LifeOS нет доступа к мессенджерам",
	}
}

// probesFor builds the text probes for the documents search.
func probesFor(expected, title string) []string {
	probes := make([]string, 0, 3)
	if trimmed := strings.TrimSpace(expected); len([]rune(trimmed)) >= 4 {
		probes = append(probes, trimmed)
	}
	words := keywords(title)
	if len(words) > 0 {
		probes = append(probes, strings.Join(words, " "))
	}
	return probes
}

// keywords keeps the meaningful words of a phrase.
func keywords(text string) []string {
	seen := make(map[string]bool, 8)
	out := make([]string, 0, 8)
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !isLetterOrDigit(r)
	}) {
		if len([]rune(word)) < 4 || seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, word)
	}
	return out
}

func isLetterOrDigit(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r >= 'а' && r <= 'я', r >= 'А' && r <= 'Я', r == 'ё', r == 'Ё':
		return true
	}
	return false
}

func matchesName(name string, words []string) bool {
	lower := strings.ToLower(name)
	for _, word := range words {
		if !strings.Contains(lower, word) {
			return false
		}
	}
	return true
}

func containsAllWords(text string, words []string) bool {
	lower := strings.ToLower(text)
	for _, word := range words {
		if !strings.Contains(lower, word) {
			return false
		}
	}
	return true
}

func sharesWords(title string, words []string) bool {
	count := 0
	for _, word := range words {
		if strings.Contains(strings.ToLower(title), word) {
			count++
		}
	}
	return count >= 2 || (len(words) == 1 && count == 1)
}

func excerptAround(text, word string, radius int) string {
	lower := strings.ToLower(text)
	index := strings.Index(lower, word)
	if index < 0 {
		if len(text) > radius {
			return text[:radius]
		}
		return text
	}
	start := index - radius
	if start < 0 {
		start = 0
	}
	end := index + len(word) + radius
	if end > len(text) {
		end = len(text)
	}
	snippet := strings.TrimSpace(text[start:end])
	return snippet
}

// negationMarkers are the phrases that mean "this is not done".
var negationMarkers = []string{
	"не готово", "не готов", "не выполнено", "не выполнена", "не сделано",
	"не завершено", "не завершена", "отменено", "отменён", "отменен", "перенесено",
}

// negationMarker returns the phrase that denies completion, if the text has one.
func negationMarker(text string) string {
	lower := strings.ToLower(text)
	for _, marker := range negationMarkers {
		if strings.Contains(lower, marker) {
			return marker
		}
	}
	return ""
}

func isProtectedName(name string) bool {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".db"), strings.HasSuffix(lower, ".db-wal"), strings.HasSuffix(lower, ".db-shm"):
		return true
	case strings.HasSuffix(lower, ".log"):
		return true
	}
	return false
}
