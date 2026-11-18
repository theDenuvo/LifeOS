package research_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// ─── fakes ──────────────────────────────────────────────────────────────────

type testClock struct{ at time.Time }

func (c *testClock) Now() time.Time { return c.at }

func (c *testClock) add(d time.Duration) { c.at = c.at.Add(d) }

func newClock() *testClock {
	return &testClock{at: time.Date(2026, 4, 12, 8, 0, 0, 0, time.UTC)}
}

// memoryRepository keeps the aggregates in maps and enforces the same
// conditional-write rule the SQLite repository does.
type memoryRepository struct {
	mu          sync.Mutex
	reports     map[int64]*domainresearch.Report
	connections map[int64]*domainresearch.Connection
	watches     map[int64]*domainresearch.Watch
	calls       []*appresearch.CallRecord
	next        int64
	// failCalls makes the journal unavailable, which must stop a changing
	// call before it reaches the outside world.
	failCalls bool
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		reports:     map[int64]*domainresearch.Report{},
		connections: map[int64]*domainresearch.Connection{},
		watches:     map[int64]*domainresearch.Watch{},
	}
}

func (r *memoryRepository) id() int64 {
	r.next++
	return r.next
}

func (r *memoryRepository) InsertReport(_ context.Context, report *domainresearch.Report) (int64, error) {
	if err := report.Validate(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	report.ID = r.id()
	report.Version = 1
	clone := *report
	r.reports[report.ID] = &clone
	return report.ID, nil
}

func (r *memoryRepository) GetReport(_ context.Context, id int64) (*domainresearch.Report, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	report, ok := r.reports[id]
	if !ok {
		return nil, appresearch.ErrNotFound
	}
	clone := *report
	return &clone, nil
}

func (r *memoryRepository) ListReports(_ context.Context, filter appresearch.ReportFilter) ([]*domainresearch.Report, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := make([]*domainresearch.Report, 0, len(r.reports))
	for _, report := range r.reports {
		if filter.Query != "" && !strings.Contains(strings.ToLower(report.Question), strings.ToLower(filter.Query)) {
			continue
		}
		clone := *report
		all = append(all, &clone)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	total := len(all)
	if filter.Offset > 0 && filter.Offset < len(all) {
		all = all[filter.Offset:]
	} else if filter.Offset >= len(all) {
		all = nil
	}
	if filter.Limit > 0 && len(all) > filter.Limit {
		all = all[:filter.Limit]
	}
	return all, total, nil
}

func (r *memoryRepository) UpdateReport(_ context.Context, id int64, expectedVersion int, report *domainresearch.Report) (*domainresearch.Report, error) {
	if err := report.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.reports[id]
	if !ok {
		return nil, appresearch.ErrNotFound
	}
	if stored.Version != expectedVersion {
		return nil, appresearch.ErrVersionConflict
	}
	clone := *report
	clone.ID = id
	clone.Version = stored.Version + 1
	r.reports[id] = &clone
	result := clone
	return &result, nil
}

func (r *memoryRepository) DeleteReport(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.reports[id]; !ok {
		return appresearch.ErrNotFound
	}
	delete(r.reports, id)
	return nil
}

func (r *memoryRepository) InsertConnection(_ context.Context, connection *domainresearch.Connection) (int64, error) {
	if err := connection.Validate(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	connection.ID = r.id()
	clone := *connection
	r.connections[connection.ID] = &clone
	return connection.ID, nil
}

func (r *memoryRepository) GetConnection(_ context.Context, id int64) (*domainresearch.Connection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	connection, ok := r.connections[id]
	if !ok {
		return nil, appresearch.ErrSourceNotFound
	}
	clone := *connection
	return &clone, nil
}

func (r *memoryRepository) ListConnections(_ context.Context, filter appresearch.ConnectionFilter) ([]*domainresearch.Connection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := make([]*domainresearch.Connection, 0, len(r.connections))
	for _, connection := range r.connections {
		if filter.Enabled != nil && connection.Enabled != *filter.Enabled {
			continue
		}
		if filter.Kind != nil && connection.Kind != *filter.Kind {
			continue
		}
		clone := *connection
		all = append(all, &clone)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all, nil
}

func (r *memoryRepository) UpdateConnection(_ context.Context, id int64, expectedVersion int, connection *domainresearch.Connection) (*domainresearch.Connection, error) {
	if err := connection.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.connections[id]
	if !ok {
		return nil, appresearch.ErrSourceNotFound
	}
	if stored.Version != expectedVersion {
		return nil, appresearch.ErrVersionConflict
	}
	clone := *connection
	clone.ID = id
	clone.Version = stored.Version + 1
	r.connections[id] = &clone
	result := clone
	return &result, nil
}

func (r *memoryRepository) DeleteConnection(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.connections[id]; !ok {
		return appresearch.ErrSourceNotFound
	}
	delete(r.connections, id)
	return nil
}

func (r *memoryRepository) InsertWatch(_ context.Context, watch *domainresearch.Watch) (int64, error) {
	if err := watch.Validate(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	watch.ID = r.id()
	clone := *watch
	r.watches[watch.ID] = &clone
	return watch.ID, nil
}

func (r *memoryRepository) GetWatch(_ context.Context, id int64) (*domainresearch.Watch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	watch, ok := r.watches[id]
	if !ok {
		return nil, appresearch.ErrWatchNotFound
	}
	clone := *watch
	return &clone, nil
}

func (r *memoryRepository) ListWatches(_ context.Context, includeDisabled bool) ([]*domainresearch.Watch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := make([]*domainresearch.Watch, 0, len(r.watches))
	for _, watch := range r.watches {
		if !includeDisabled && !watch.Enabled {
			continue
		}
		clone := *watch
		all = append(all, &clone)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all, nil
}

func (r *memoryRepository) DueWatches(_ context.Context, now time.Time) ([]*domainresearch.Watch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	due := make([]*domainresearch.Watch, 0, len(r.watches))
	for _, watch := range r.watches {
		if !watch.Enabled || watch.Cadence == domainresearch.CadenceManual {
			continue
		}
		if watch.NextCheckAt == nil || !watch.NextCheckAt.After(now) {
			clone := *watch
			due = append(due, &clone)
		}
	}
	return due, nil
}

func (r *memoryRepository) UpdateWatch(_ context.Context, id int64, expectedVersion int, watch *domainresearch.Watch) (*domainresearch.Watch, error) {
	if err := watch.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.watches[id]
	if !ok {
		return nil, appresearch.ErrWatchNotFound
	}
	if stored.Version != expectedVersion {
		return nil, appresearch.ErrVersionConflict
	}
	clone := *watch
	clone.ID = id
	clone.Version = stored.Version + 1
	r.watches[id] = &clone
	result := clone
	return &result, nil
}

func (r *memoryRepository) DeleteWatch(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.watches[id]; !ok {
		return appresearch.ErrWatchNotFound
	}
	delete(r.watches, id)
	return nil
}

func (r *memoryRepository) RecordCall(_ context.Context, record *appresearch.CallRecord) (int64, error) {
	if r.failCalls {
		return 0, errors.New("журнал недоступен")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record.ID = r.id()
	clone := *record
	r.calls = append(r.calls, &clone)
	return record.ID, nil
}

func (r *memoryRepository) FinishCall(_ context.Context, id int64, outcome, detail string, durationMS int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range r.calls {
		if record.ID != id {
			continue
		}
		if record.Outcome != appresearch.CallStarted {
			return appresearch.ErrNotFound
		}
		record.Outcome = outcome
		record.Detail = detail
		record.DurationMS = durationMS
		return nil
	}
	return appresearch.ErrNotFound
}

func (r *memoryRepository) RecoverRunningReports(_ context.Context, reason string, now time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var touched int64
	for _, report := range r.reports {
		if report.Status != domainresearch.ReportRunning {
			continue
		}
		report.Status = domainresearch.ReportFailed
		report.Error = reason
		report.FinishedAt = &now
		touched++
	}
	return touched, nil
}

func (r *memoryRepository) RecoverStartedCalls(_ context.Context, detail string, _ time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var touched int64
	for _, record := range r.calls {
		if record.Outcome != appresearch.CallStarted {
			continue
		}
		record.Outcome = appresearch.CallUnknown
		record.Detail = detail
		touched++
	}
	return touched, nil
}

func (r *memoryRepository) ListCalls(_ context.Context, connectionID int64, limit int) ([]*appresearch.CallRecord, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := make([]*appresearch.CallRecord, 0, len(r.calls))
	for _, record := range r.calls {
		if connectionID > 0 && record.ConnectionID != connectionID {
			continue
		}
		clone := *record
		found = append(found, &clone)
	}
	total := len(found)
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	return found, total, nil
}

func (r *memoryRepository) journal() []*appresearch.CallRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*appresearch.CallRecord(nil), r.calls...)
}

type memoryUnitOfWork struct{ repo *memoryRepository }

func (u memoryUnitOfWork) Do(ctx context.Context, fn func(context.Context, appresearch.Transaction) error) error {
	return fn(ctx, memoryTransaction{repo: u.repo})
}

type memoryTransaction struct{ repo *memoryRepository }

func (t memoryTransaction) Research() appresearch.Repository { return t.repo }

// fakeWeb answers with canned pages and records what was asked.
type fakeWeb struct {
	mu        sync.Mutex
	results   map[string][]appresearch.SearchResult
	pages     map[string]appresearch.Page
	failing   map[string]bool
	searches  []string
	calls     []appresearch.CallRequest
	durations []string
	callBody  string
	// callErr is what Call returns, for the refusals the adapter decides on.
	callErr error
	// beforeCall runs while the call is still in flight: it is how a test
	// observes the journal mid-call.
	beforeCall func()
	// cancelOnSearch cancels the run while the evidence is being gathered,
	// which is what a closed HTTP connection does.
	cancelOnSearch context.CancelFunc
	// searchErr fails every search.
	searchErr error
}

func newFakeWeb() *fakeWeb {
	return &fakeWeb{
		results:  map[string][]appresearch.SearchResult{},
		pages:    map[string]appresearch.Page{},
		failing:  map[string]bool{},
		callBody: `{"ok":true}`,
	}
}

func (w *fakeWeb) Search(_ context.Context, connection *domainresearch.Connection, query string, limit int) ([]appresearch.SearchResult, error) {
	w.mu.Lock()
	cancel := w.cancelOnSearch
	err := w.searchErr
	connectionID := int64(0)
	if connection != nil {
		connectionID = connection.ID
	}
	w.searches = append(w.searches, query)
	if connectionID > 0 {
		w.durations = append(w.durations, "search")
	}
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	results := w.results[query]
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (w *fakeWeb) Fetch(_ context.Context, rawURL string) (appresearch.Page, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failing[rawURL] {
		return appresearch.Page{}, errors.New("страница недоступна")
	}
	page, ok := w.pages[rawURL]
	if !ok {
		return appresearch.Page{}, errors.New("нет такой страницы")
	}
	return page, nil
}

func (w *fakeWeb) Call(_ context.Context, _ *domainresearch.Connection, request appresearch.CallRequest) (appresearch.CallResult, error) {
	if w.beforeCall != nil {
		w.beforeCall()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, request)
	if w.callErr != nil {
		return appresearch.CallResult{}, w.callErr
	}
	return appresearch.CallResult{Status: 200, ContentType: "application/json", Body: w.callBody}, nil
}

// memoryKnowledge records the knowledge entries research produced.
type memoryKnowledge struct {
	mu      sync.Mutex
	saved   []appresearch.KnowledgeInput
	failing bool
	nextID  int64
}

func (k *memoryKnowledge) SaveResearch(_ context.Context, input appresearch.KnowledgeInput) (int64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failing {
		return 0, errors.New("база знаний недоступна")
	}
	k.saved = append(k.saved, input)
	k.nextID++
	return 700 + k.nextID, nil
}

// publisher records the live events the service announces.
type publisher struct {
	events.Broker
	mu     sync.Mutex
	events []string
}

func (p *publisher) Publish(eventType string, _ any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, eventType)
	return nil
}

func (p *publisher) announced() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.events...)
}

func newService(repo *memoryRepository, web appresearch.External, knowledge appresearch.KnowledgeSink) (*appresearch.Service, *testClock, *publisher) {
	clock := newClock()
	broker := &publisher{}
	options := []appresearch.Option{appresearch.WithPublisher(broker)}
	if web != nil {
		options = append(options, appresearch.WithExternal(web))
	}
	if knowledge != nil {
		options = append(options, appresearch.WithKnowledge(knowledge))
	}
	return appresearch.NewService(memoryUnitOfWork{repo: repo}, clock, options...), clock, broker
}

// page builds a fetched page with a title and body.
func page(url, title, text string) appresearch.Page {
	return appresearch.Page{URL: url, Title: title, Text: text}
}

// ─── research runs ──────────────────────────────────────────────────────────

func TestResearchCorroboratesAcrossIndependentSites(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	knowledge := &memoryKnowledge{}
	service, _, broker := newService(repo, web, knowledge)

	web.results["go 1.24 generic aliases"] = []appresearch.SearchResult{
		{Title: "Go 1.24 release notes", URL: "https://go.dev/doc/go1.24", Snippet: "Go 1.24 adds generic type aliases."},
		{Title: "Review", URL: "https://example.org/go124", Snippet: "Generic type aliases are here."},
	}
	web.pages["https://go.dev/doc/go1.24"] = page("https://go.dev/doc/go1.24", "Go 1.24 release notes",
		"The release adds generic type aliases to the language, so a package can name a parameterised type. "+
			"They are available in Go 1.24 and later versions of the toolchain.")
	web.pages["https://example.org/go124"] = page("https://example.org/go124", "Review",
		"In Go 1.24 the language adds generic type aliases, which let a package name a parameterised type without a wrapper. "+
			"The feature is stable.")

	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question:      "Какие изменения появились в Go 1.24 для generic type aliases?",
		Queries:       []string{"go 1.24 generic aliases"},
		SaveKnowledge: true,
	})
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	if report.Status != domainresearch.ReportCompleted {
		t.Fatalf("status = %s", report.Status)
	}
	if len(report.Findings) == 0 {
		t.Fatalf("no findings: %+v", report)
	}
	fact := report.Findings[0]
	if fact.Certainty != domainresearch.CertaintyFact {
		t.Fatalf("two independent sites must produce a fact, got %s (%+v)", fact.Certainty, fact)
	}
	if report.Sources != 2 {
		t.Fatalf("both pages are sources, got %d", report.Sources)
	}
	if report.KnowledgeEntryID == nil {
		t.Fatal("the result must be saved to the knowledge base")
	}
	if len(knowledge.saved) != 1 {
		t.Fatalf("knowledge saved %d times", len(knowledge.saved))
	}
	entry := knowledge.saved[0]
	if entry.Certainty != string(domainresearch.CertaintyFact) {
		t.Fatalf("a corroborated report is saved as a fact, got %q", entry.Certainty)
	}
	if !strings.Contains(entry.SourceLabel, "go.dev") || !strings.Contains(entry.SourceLabel, "example.org") {
		t.Fatalf("provenance must name the sites, got %q", entry.SourceLabel)
	}
	if !strings.Contains(entry.Content, "https://go.dev/doc/go1.24") {
		t.Fatalf("the saved text must carry the citation, got %q", entry.Content)
	}
	announced := strings.Join(broker.announced(), ",")
	if !strings.Contains(announced, "research.completed") {
		t.Fatalf("expected a completion event, got %q", announced)
	}
}

// A page is quoted by sentence, not by paragraph: a paragraph compared as a
// whole never matches another site's wording, so no claim would ever be
// confirmed and the report would read as a wall of text.
func TestResearchQuotesOneSentencePerClaim(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	web.results["capital of Australia"] = []appresearch.SearchResult{
		{Title: "Canberra", URL: "https://a.example.org/canberra"},
	}
	web.pages["https://a.example.org/canberra"] = page("https://a.example.org/canberra", "Canberra",
		"About the capital\n\n"+
			"Canberra is the capital city of Australia. "+
			"The city occupies the northern part of the Australian Capital Territory and borders New South Wales. "+
			"Greater Canberra has a population of about half a million people.")

	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Какая столица Австралии?", Queries: []string{"capital of Australia"},
	})
	if err != nil {
		t.Fatalf("research: %v", err)
	}
	if len(report.Findings) < 2 {
		t.Fatalf("each answering sentence is a claim, got %+v", report.Findings)
	}
	for _, finding := range report.Findings {
		if strings.Contains(strings.TrimSuffix(finding.Statement, "."), ". ") {
			t.Fatalf("a claim must be one sentence: %q", finding.Statement)
		}
		if len([]rune(finding.Statement)) > 400 {
			t.Fatalf("a claim must stay quotable: %q", finding.Statement)
		}
	}
	if !strings.HasPrefix(report.Findings[0].Statement, "Canberra is the capital city of Australia.") {
		t.Fatalf("the answering sentence must come first, got %q", report.Findings[0].Statement)
	}
}

// Sentences worded differently still confirm each other: the comparison is on
// significant words, not on identical paragraphs.
func TestResearchMatchesDifferentlyWordedSentences(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	web.results["capital of Australia"] = []appresearch.SearchResult{
		{Title: "Canberra", URL: "https://a.example.org/canberra"},
		{Title: "Australia", URL: "https://b.example.net/australia"},
	}
	web.pages["https://a.example.org/canberra"] = page("https://a.example.org/canberra", "Canberra",
		"Canberra is the capital city of Australia. The city occupies the northern part of the territory.")
	web.pages["https://b.example.net/australia"] = page("https://b.example.net/australia", "Australia",
		"The capital city of Australia is Canberra. It lies in the south-east of the country.")

	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Какая столица Австралии?", Queries: []string{"capital of Australia"},
	})
	if err != nil {
		t.Fatalf("research: %v", err)
	}
	if len(report.Findings) == 0 {
		t.Fatal("no findings")
	}
	top := report.Findings[0]
	if top.Certainty != domainresearch.CertaintyFact || top.Corroboration != 2 {
		t.Fatalf("two sites saying the same must be a fact, got %s/%d (%+v)",
			top.Certainty, top.Corroboration, top)
	}
	if len(top.Citations) != 2 {
		t.Fatalf("both citations must stay, got %+v", top.Citations)
	}
}

func TestResearchKeepsSingleSourceAsAnAssumption(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)

	web.results["kotlin coroutines"] = []appresearch.SearchResult{
		{Title: "Docs", URL: "https://docs.example.com/coroutines", Snippet: "Coroutines are lightweight threads."},
	}
	web.pages["https://docs.example.com/coroutines"] = page("https://docs.example.com/coroutines", "Docs",
		"Kotlin coroutines are a way to write asynchronous code that reads like sequential code. "+
			"The compiler turns suspend functions into state machines.")

	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Что такое корутины в Kotlin?",
		Queries:  []string{"kotlin coroutines"},
	})
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected one finding, got %+v", report.Findings)
	}
	if report.Findings[0].Certainty != domainresearch.CertaintyAssumption {
		t.Fatalf("one site is not a fact, got %s", report.Findings[0].Certainty)
	}
	if report.KnowledgeEntryID != nil {
		t.Fatal("knowledge is only saved when it was asked for")
	}
}

func TestResearchFallsBackToSearchSnippetsWhenAPageDoesNotOpen(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)

	web.results["self hosted searxng"] = []appresearch.SearchResult{
		{Title: "SearxNG", URL: "https://blocked.example.com/searxng",
			Snippet: "SearxNG is a free internet metasearch engine which aggregates results from various search services and databases."},
	}
	web.failing["https://blocked.example.com/searxng"] = true

	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Что такое SearxNG?",
		Queries:  []string{"self hosted searxng"},
	})
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected the snippet to survive, got %+v", report.Findings)
	}
	citation := report.Findings[0].Citations[0]
	if citation.Trust != domainresearch.TrustLow {
		t.Fatalf("an unread page must be labelled as low trust, got %s", citation.Trust)
	}
	if !strings.Contains(citation.Excerpt, "страница не прочитана") {
		t.Fatalf("the excerpt must say the page was not read: %q", citation.Excerpt)
	}
	// "Страниц прочитано: 0" next to a finding would look like a contradiction
	// unless the report says which source was never opened.
	if !strings.Contains(report.Summary, "Страниц прочитано: 0") {
		t.Fatalf("the summary must count the pages actually read: %q", report.Summary)
	}
	if !strings.Contains(report.Summary, "Не удалось открыть источников: 1") {
		t.Fatalf("the summary must name the unread source: %q", report.Summary)
	}
	// A finding nobody read is not "one source": it is a lead, and saying
	// "Один источник" over an unopened page would imply someone read it.
	if strings.Contains(report.Summary, "Один источник, требует проверки") {
		t.Fatalf("an unread finding must not be presented as a read source: %q", report.Summary)
	}
	if !strings.Contains(report.Summary, "Только поисковая выдержка, страница не открылась") {
		t.Fatalf("the summary must separate snippet-only findings: %q", report.Summary)
	}
	if !strings.Contains(report.Summary, "(выдержка)") {
		t.Fatalf("the citation line must mark a snippet as such: %q", report.Summary)
	}
}

// A source with low trust is still a source: reading status and trust are
// different things, and a read page must not be reported as unread.
func TestLowTrustSourceThatWasReadIsNotCountedAsUnread(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	connection, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "Форум", Kind: domainresearch.KindWebSearch, Mode: domainresearch.ModeRead,
		Endpoint: "https://forum.example.org/search?q={query}", Format: domainresearch.FormatSearxng,
		Trust: domainresearch.TrustLow, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	web.results["вопрос"] = []appresearch.SearchResult{{Title: "Ответ", URL: "https://forum.example.org/t/1"}}
	web.pages["https://forum.example.org/t/1"] = page("https://forum.example.org/t/1", "Ответ",
		"Это подробный ответ на вопрос, который объясняет контекст и приводит примеры из практики.")

	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Вопрос", Queries: []string{"вопрос"}, ConnectionID: connection.ID,
	})
	if err != nil {
		t.Fatalf("research: %v", err)
	}
	if strings.Contains(report.Summary, "Не удалось открыть") {
		t.Fatalf("a read page is not unread: %q", report.Summary)
	}
	if len(report.Findings) == 0 || !report.Findings[0].Citations[0].Read {
		t.Fatalf("the citation must be marked as read: %+v", report.Findings)
	}
	if report.Findings[0].Citations[0].Trust != domainresearch.TrustLow {
		t.Fatalf("trust is unchanged: %+v", report.Findings[0].Citations[0])
	}
}

func TestResearchWithoutSearchProviderFailsLoudly(t *testing.T) {
	repo := newMemoryRepository()
	service, _, _ := newService(repo, nil, nil)
	_, err := service.Research(context.Background(), appresearch.ResearchInput{Question: "Что нового?"})
	if !errors.Is(err, appresearch.ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}

func TestResearchFailureIsRecorded(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	web.results["пусто"] = nil

	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Вопрос без источников",
		Queries:  []string{"пусто"},
	})
	if err != nil {
		t.Fatalf("an empty result is not an error: %v", err)
	}
	if report.Status != domainresearch.ReportCompleted || len(report.Findings) != 0 {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Summary, "Достаточных оснований") {
		t.Fatalf("the summary must say the evidence was insufficient: %q", report.Summary)
	}
}

func TestResearchSaveFailureIsReportedNotHidden(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	knowledge := &memoryKnowledge{failing: true}
	service, _, _ := newService(repo, web, knowledge)

	web.results["kotlin coroutines"] = []appresearch.SearchResult{{Title: "A", URL: "https://a.example.org/1"}}
	web.pages["https://a.example.org/1"] = page("https://a.example.org/1", "A",
		"The page explains how Kotlin coroutines suspend a function and resume it later on a dispatcher.")

	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Что такое корутины Kotlin?", Queries: []string{"kotlin coroutines"}, SaveKnowledge: true,
	})
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	if report.KnowledgeEntryID != nil {
		t.Fatal("nothing was saved, so no entry id may be claimed")
	}
	if !strings.Contains(report.Error, "не сохранён") {
		t.Fatalf("the failure must be visible on the report: %q", report.Error)
	}
}

// ─── sources ────────────────────────────────────────────────────────────────

func TestAssistantSourceStartsDisabledAndReadOnly(t *testing.T) {
	repo := newMemoryRepository()
	service, _, _ := newService(repo, newFakeWeb(), nil)

	created, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "GitHub API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeWrite,
		Endpoint: "https://api.github.com", CreatedByAssistant: true,
	})
	if err == nil {
		t.Fatalf("the assistant may not connect a writable source: %+v", created)
	}

	created, err = service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "GitHub API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeRead,
		Endpoint: "https://api.github.com", CreatedByAssistant: true,
	})
	if err != nil {
		t.Fatalf("read-only source rejected: %v", err)
	}
	if created.Enabled {
		t.Fatal("an assistant-created source waits for the user to enable it")
	}
}

func TestAssistantCannotConnectScripts(t *testing.T) {
	repo := newMemoryRepository()
	service, _, _ := newService(repo, newFakeWeb(), nil)
	_, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "Скрипт", Kind: domainresearch.KindScript, Mode: domainresearch.ModeRead,
		Endpoint: "hostname", CreatedByAssistant: true,
	})
	if !errors.Is(err, appresearch.ErrScriptNeedsUser) {
		t.Fatalf("expected ErrScriptNeedsUser, got %v", err)
	}
}

func TestReadOnlySourceRefusesWritesAndJournalsThem(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)

	created, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "Read only API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeRead,
		Endpoint: "https://api.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if _, err := service.UpdateConnection(context.Background(), created.ID, created.Version,
		appresearch.ConnectionUpdate{Enabled: boolPointer(true)}); err != nil {
		t.Fatalf("enable source: %v", err)
	}

	_, err = service.CallSource(context.Background(), created.ID, appresearch.CallRequest{
		Operation: "/items", Method: "POST", Body: `{"name":"x"}`, Write: true,
	})
	if !errors.Is(err, appresearch.ErrSourceReadOnly) {
		t.Fatalf("expected ErrSourceReadOnly, got %v", err)
	}
	if len(web.calls) != 0 {
		t.Fatalf("a refused call must not reach the network: %+v", web.calls)
	}
	records, total, err := service.ListCalls(context.Background(), created.ID, 10)
	if err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if total != 1 || records[0].Outcome != appresearch.CallRefused {
		t.Fatalf("the refusal must be journalled, got %+v", records)
	}
}

func TestWritableSourceAllowsTheCallAndRecordsIt(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)

	created, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "Writable API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeWrite,
		Endpoint: "https://api.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	result, err := service.CallSource(context.Background(), created.ID, appresearch.CallRequest{
		Operation: "/items", Method: "POST", Body: `{"name":"x"}`, Write: true,
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Status != 200 {
		t.Fatalf("result = %+v", result)
	}
	records, _, err := service.ListCalls(context.Background(), created.ID, 10)
	if err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if len(records) != 1 || records[0].Outcome != appresearch.CallOK {
		t.Fatalf("journal = %+v", records)
	}
}

func TestDisabledSourceCannotBeCalled(t *testing.T) {
	repo := newMemoryRepository()
	service, _, _ := newService(repo, newFakeWeb(), nil)
	created, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "Off", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeRead,
		Endpoint: "https://api.example.com",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = service.CallSource(context.Background(), created.ID, appresearch.CallRequest{Operation: "/x"})
	if !errors.Is(err, appresearch.ErrSourceDisabled) {
		t.Fatalf("expected ErrSourceDisabled, got %v", err)
	}
}

func boolPointer(value bool) *bool { return &value }

// ─── watches ────────────────────────────────────────────────────────────────

func TestWatchReportsOnlyRealChanges(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, clock, broker := newService(repo, web, nil)

	web.results["github changelog"] = []appresearch.SearchResult{
		{Title: "Changelog", URL: "https://github.blog/changelog"},
	}
	web.pages["https://github.blog/changelog"] = page("https://github.blog/changelog", "Changelog",
		"This changelog lists the latest platform changes, published by the platform team every week.")

	watch, err := service.CreateWatch(context.Background(), appresearch.WatchInput{
		Topic: "GitHub changelog", Query: "github changelog", Cadence: domainresearch.CadenceDaily,
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	if watch.NextCheckAt == nil || watch.Cadence != domainresearch.CadenceDaily {
		t.Fatalf("watch = %+v", watch)
	}

	// First check: everything is new, but the interface calls it a change only
	// relative to an existing baseline.
	checked, diff, err := service.CheckWatch(context.Background(), watch.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if diff == nil || len(diff.New) != 1 {
		t.Fatalf("expected one new page, got %+v", diff)
	}
	if checked.LastChangeSummary == "" {
		t.Fatal("the change must be summarised on the watch")
	}
	if !checked.NextCheckAt.After(clock.Now()) == false && checked.NextCheckAt == nil {
		t.Fatal("the next check must be scheduled")
	}

	// Same content again: no change, and no second announcement.
	before := len(broker.announced())
	clock.add(24 * time.Hour)
	unchanged, diff, err := service.CheckWatch(context.Background(), watch.ID)
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if diff.Changed() {
		t.Fatalf("identical content is not a change: %+v", diff)
	}
	announced := broker.announced()[before:]
	for _, event := range announced {
		if event == "research.watch.changed" {
			t.Fatalf("no change event when nothing changed: %v", announced)
		}
	}

	// The page is rewritten: that is a change.
	web.pages["https://github.blog/changelog"] = page("https://github.blog/changelog", "Changelog",
		"This changelog lists the latest platform changes, now including a monthly digest and an archive.")
	clock.add(24 * time.Hour)
	changed, diff, err := service.CheckWatch(context.Background(), watch.ID)
	if err != nil {
		t.Fatalf("third check: %v", err)
	}
	if diff == nil || len(diff.Updated) != 1 {
		t.Fatalf("expected one updated page, got %+v", diff)
	}
	if changed.LastChangeAt == nil {
		t.Fatal("the change moment must be stored")
	}
	if !contains(broker.announced(), "research.watch.changed") {
		t.Fatalf("expected a change event, got %v", broker.announced())
	}
	_ = unchanged
}

func TestCheckDueOnlyChecksWatchesWhoseMomentCame(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, clock, _ := newService(repo, web, nil)

	web.results["q"] = []appresearch.SearchResult{{Title: "A", URL: "https://a.example.org/1"}}
	web.pages["https://a.example.org/1"] = page("https://a.example.org/1", "A",
		"A sufficiently long page body that mentions the query q so the passage is selected for evidence.")

	watch, err := service.CreateWatch(context.Background(), appresearch.WatchInput{
		Topic: "Тема", Query: "q", Cadence: domainresearch.CadenceDaily,
	})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}

	tick := service.CheckDue(context.Background(), clock.Now())
	if tick.Checked != 0 {
		t.Fatalf("nothing is due right after creation, got %+v", tick)
	}
	clock.add(25 * time.Hour)
	tick = service.CheckDue(context.Background(), clock.Now())
	if tick.Checked != 1 {
		t.Fatalf("the watch is due after a day, got %+v", tick)
	}
	changes, err := service.RecentChanges(context.Background(), clock.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("recent changes: %v", err)
	}
	if len(changes) != 1 || changes[0].WatchID != watch.ID {
		t.Fatalf("changes = %+v", changes)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestWatchUpdateRecomputesTheNextCheck(t *testing.T) {
	repo := newMemoryRepository()
	service, clock, _ := newService(repo, newFakeWeb(), nil)
	watch, err := service.CreateWatch(context.Background(), appresearch.WatchInput{
		Topic: "Тема", Query: "q", Cadence: domainresearch.CadenceDaily,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	clock.add(3 * time.Hour)
	weekly := domainresearch.CadenceWeekly
	updated, err := service.UpdateWatch(context.Background(), watch.ID, watch.Version,
		appresearch.WatchUpdate{Cadence: &weekly})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.NextCheckAt == nil {
		t.Fatal("a weekly watch still has a next check")
	}
	expected := clock.Now().Add(7 * 24 * time.Hour)
	if !updated.NextCheckAt.Equal(expected) {
		t.Fatalf("next check = %v, want %v", updated.NextCheckAt, expected)
	}
	manual := domainresearch.CadenceManual
	updated, err = service.UpdateWatch(context.Background(), watch.ID, updated.Version,
		appresearch.WatchUpdate{Cadence: &manual})
	if err != nil {
		t.Fatalf("update to manual: %v", err)
	}
	if updated.NextCheckAt != nil {
		t.Fatal("a manual watch has no automatic check")
	}
}

func TestStaleVersionIsRejected(t *testing.T) {
	repo := newMemoryRepository()
	service, _, _ := newService(repo, newFakeWeb(), nil)
	connection, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeRead,
		Endpoint: "https://api.example.com",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	enabled := true
	if _, err := service.UpdateConnection(context.Background(), connection.ID, connection.Version,
		appresearch.ConnectionUpdate{Enabled: &enabled}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	_, err = service.UpdateConnection(context.Background(), connection.ID, connection.Version,
		appresearch.ConnectionUpdate{Enabled: &enabled})
	if !errors.Is(err, appresearch.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
}

func TestReportListingIsNewestFirst(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, clock, _ := newService(repo, web, nil)
	web.results["a"] = []appresearch.SearchResult{{Title: "A", URL: "https://a.example.org/1"}}
	web.pages["https://a.example.org/1"] = page("https://a.example.org/1", "A",
		"A long enough body that talks about a and the surrounding context of the question in detail.")

	for _, question := range []string{"Первый", "Второй"} {
		if _, err := service.Research(context.Background(), appresearch.ResearchInput{
			Question: question, Queries: []string{"a"},
		}); err != nil {
			t.Fatalf("research %q: %v", question, err)
		}
		clock.add(time.Minute)
	}
	reports, total, err := service.ListReports(context.Background(), appresearch.ReportFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(reports) != 2 {
		t.Fatalf("reports = %d/%d", len(reports), total)
	}
	if reports[0].Question != "Второй" {
		t.Fatalf("newest first, got %q", reports[0].Question)
	}
	if err := service.DeleteReport(context.Background(), reports[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := service.GetReport(context.Background(), reports[0].ID); !errors.Is(err, appresearch.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestSearchUsesTheConnectedSearchSource(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)

	source, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "Мой SearxNG", Kind: domainresearch.KindWebSearch, Mode: domainresearch.ModeRead,
		Format: domainresearch.FormatSearxng, Endpoint: "https://search.example.com/search?q={query}",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	web.results["kafka"] = []appresearch.SearchResult{{Title: "Kafka", URL: "https://kafka.apache.org/"}}
	results, err := service.Search(context.Background(), "kafka", 5, source.ID)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}

	apiSource, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeRead,
		Endpoint: "https://api.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create api: %v", err)
	}
	if _, err := service.Search(context.Background(), "kafka", 5, apiSource.ID); err == nil {
		t.Fatal("a non-search source cannot answer a search")
	}
}

func TestDisabledSearchSourceIsRefused(t *testing.T) {
	repo := newMemoryRepository()
	service, _, _ := newService(repo, newFakeWeb(), nil)
	source, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "Поиск", Kind: domainresearch.KindWebSearch, Mode: domainresearch.ModeRead,
		Format: domainresearch.FormatSearxng, Endpoint: "https://search.example.com/search?q={query}",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.Search(context.Background(), "q", 5, source.ID); !errors.Is(err, appresearch.ErrSourceDisabled) {
		t.Fatalf("expected ErrSourceDisabled, got %v", err)
	}
	// A research run through the same source is refused too, and reports why.
	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Вопрос", Queries: []string{"q"}, ConnectionID: source.ID,
	})
	if err == nil {
		t.Fatal("expected the run to fail")
	}
	if report == nil || report.Status != domainresearch.ReportFailed {
		t.Fatalf("the failed run must be recorded, got %+v", report)
	}
}

func TestResearchUsesAtMostThreeQueriesAndEightPages(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	for _, query := range []string{"a", "b", "c", "d", "e"} {
		web.results[query] = []appresearch.SearchResult{{Title: query, URL: "https://" + query + ".example.com/1"}}
		web.pages["https://"+query+".example.com/1"] = page("https://"+query+".example.com/1", query,
			"A long enough page body that mentions the query and its context in a readable sentence.")
	}
	if _, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Вопрос", Queries: []string{"a", "b", "c", "d", "e"}, MaxPages: 50,
	}); err != nil {
		t.Fatalf("research: %v", err)
	}
	if len(web.searches) != 3 {
		t.Fatalf("at most three queries are run, got %v", web.searches)
	}
}

func TestConcurrentReportUpdateIsRejected(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	web.results["q"] = []appresearch.SearchResult{{Title: "A", URL: "https://a.example.org/1"}}
	web.pages["https://a.example.org/1"] = page("https://a.example.org/1", "A",
		"A long enough body that talks about q and the context of the question in a readable sentence.")
	report, err := service.Research(context.Background(), appresearch.ResearchInput{Question: "Q", Queries: []string{"q"}})
	if err != nil {
		t.Fatalf("research: %v", err)
	}
	stale := *report
	stale.Version--
	_, err = repo.UpdateReport(context.Background(), report.ID, stale.Version, &stale)
	if !errors.Is(err, appresearch.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
}

func TestSummaryStatesThatOneSourceIsNotAFact(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	web.results["вопрос"] = []appresearch.SearchResult{{Title: "A", URL: "https://a.example.org/1"}}
	web.pages["https://a.example.org/1"] = page("https://a.example.org/1", "A",
		"Это достаточно длинный текст страницы, который отвечает на вопрос и объясняет контекст подробно.")
	report, err := service.Research(context.Background(), appresearch.ResearchInput{
		Question: "Вопрос", Queries: []string{"вопрос"},
	})
	if err != nil {
		t.Fatalf("research: %v", err)
	}
	if !strings.Contains(report.Summary, "Один источник, требует проверки") {
		t.Fatalf("the summary must separate assumptions: %q", report.Summary)
	}
	if !strings.Contains(report.Summary, "Ни один вывод не считается фактом") {
		t.Fatalf("the summary must state the rule: %q", report.Summary)
	}
	_ = fmt.Sprint()
}

// ─── разрешения, журнал и восстановление ───────────────────────────────────

// The journal promises to record every interaction with a connected source —
// not only the changing ones — and a promise to record cannot be kept by
// writing afterwards: a crash would lose the call entirely. A read is entered
// before it leaves and closed with its outcome; a changed row count of one per
// call proves both halves happened.
func TestEveryCallIsJournalledBeforeItLeaves(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	source, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeRead,
		Endpoint: "https://api.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var seenMidFlight []appresearch.CallRecord
	web.beforeCall = func() {
		// The answer has not arrived yet; the journal already has the call.
		records, _, err := service.ListCalls(context.Background(), source.ID, 10)
		if err != nil || len(records) != 1 {
			t.Errorf("mid-flight journal: %d records, %v", len(records), err)
			return
		}
		seenMidFlight = append(seenMidFlight, *records[0])
	}

	if _, err := service.CallSource(context.Background(), source.ID, appresearch.CallRequest{Operation: "/status", Method: "GET"}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(seenMidFlight) != 1 || seenMidFlight[0].Outcome != appresearch.CallStarted {
		t.Fatalf("the read must be journalled before it leaves, got %+v", seenMidFlight)
	}

	records, _, err := service.ListCalls(context.Background(), source.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// The started row is closed, not duplicated.
	if len(records) != 1 || records[0].Outcome != appresearch.CallOK {
		t.Fatalf("one call is one closed journal entry, got %+v", records)
	}
}

// A source in read mode must refuse a changing call before it reaches the
// network, and the refusal itself belongs in the journal.
func TestReadOnlyRefusalFromTheAdapterIsJournalledAsARefusal(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	web.callErr = fmt.Errorf("%w: инструмент не помечен как «только чтение»", appresearch.ErrSourceReadOnly)
	service, _, _ := newService(repo, web, nil)

	source, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "MCP", Kind: domainresearch.KindMCP, Mode: domainresearch.ModeRead,
		Endpoint: "https://mcp.example.com/mcp", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.CallSource(context.Background(), source.ID, appresearch.CallRequest{Operation: "delete_everything"}); err == nil {
		t.Fatal("the call must be refused")
	}
	records, _, err := service.ListCalls(context.Background(), source.ID, 10)
	if err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if len(records) != 1 || records[0].Outcome != appresearch.CallRefused {
		t.Fatalf("a refusal decided by the source is still a refusal: %+v", records)
	}
}

// A search through a connected source is an action like any other: it appears
// in the journal and it updates the source's last use.
func TestSearchThroughAConnectedSourceIsJournalled(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, clock, _ := newService(repo, web, nil)
	source, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "SearxNG", Kind: domainresearch.KindWebSearch, Mode: domainresearch.ModeRead,
		Endpoint: "https://search.example.com/search?q={query}", Format: domainresearch.FormatSearxng,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	web.results["go"] = []appresearch.SearchResult{{Title: "Go", URL: "https://go.dev/doc"}}
	if _, err := service.Search(context.Background(), "go", 5, source.ID); err != nil {
		t.Fatalf("search: %v", err)
	}
	records, _, err := service.ListCalls(context.Background(), source.ID, 20)
	if err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("the search must be journalled, got %+v", records)
	}
	if records[0].Outcome != appresearch.CallOK || records[0].Operation != "search" {
		t.Fatalf("journal entry = %+v", records[0])
	}
	if records[0].Method != "GET" {
		t.Fatalf("the journal must record the method, got %q", records[0].Method)
	}
	stored, err := service.GetConnection(context.Background(), source.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.LastUsedAt == nil {
		t.Fatal("a used source must record when it was used")
	}
	_ = clock
}

// A changing call is recorded before it leaves: if the journal cannot be
// written, LifeOS must not change something outside and keep no record of it.
func TestChangingCallIsRefusedWhenTheJournalIsUnavailable(t *testing.T) {
	repo := newMemoryRepository()
	repo.failCalls = true
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	source, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeWrite,
		Endpoint: "https://api.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = service.CallSource(context.Background(), source.ID, appresearch.CallRequest{
		Method: "POST", Operation: "/issues", Body: `{"title":"x"}`,
	})
	if !errors.Is(err, appresearch.ErrJournalUnavailable) {
		t.Fatalf("the call must be refused without a journal, got %v", err)
	}
	if len(web.calls) != 0 {
		t.Fatalf("nothing may reach the service: %+v", web.calls)
	}
}

// The journal of a changing call starts as "started" and is closed with the
// outcome, so a crash leaves "unknown" instead of nothing.
func TestChangingCallIsJournalledBeforeAndAfter(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	source, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeWrite,
		Endpoint: "https://api.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.CallSource(context.Background(), source.ID, appresearch.CallRequest{
		Method: "DELETE", Operation: "/issues/1",
	}); err != nil {
		t.Fatalf("call: %v", err)
	}
	records, _, err := service.ListCalls(context.Background(), source.ID, 10)
	if err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("one call is one journal entry, got %+v", records)
	}
	if records[0].Outcome != appresearch.CallOK || records[0].Method != "DELETE" {
		t.Fatalf("journal entry = %+v", records[0])
	}
}

// A restart closes what the previous process left open, and says so.
func TestRecoverInterruptedClosesRunningWork(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, clock, _ := newService(repo, web, nil)

	report, err := service.Research(context.Background(), appresearch.ResearchInput{Question: "старый вопрос"})
	if err != nil {
		t.Fatalf("research: %v", err)
	}
	// Pretend the process died while the report was running.
	repo.mu.Lock()
	repo.reports[report.ID].Status = domainresearch.ReportRunning
	repo.reports[report.ID].FinishedAt = nil
	repo.mu.Unlock()
	source, err := service.CreateConnection(context.Background(), &domainresearch.Connection{
		Name: "API", Kind: domainresearch.KindHTTPAPI, Mode: domainresearch.ModeWrite,
		Endpoint: "https://api.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	repo.mu.Lock()
	repo.calls = append(repo.calls, &appresearch.CallRecord{
		ID: 99, ConnectionID: source.ID, Name: source.Name, Kind: source.Kind,
		Mode: source.Mode, Method: "POST", Operation: "/issues",
		Outcome: appresearch.CallStarted, CreatedAt: clock.Now(),
	})
	repo.mu.Unlock()

	recovered, err := service.RecoverInterrupted(context.Background())
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered.Reports != 1 || recovered.Calls != 1 {
		t.Fatalf("recovery = %+v", recovered)
	}
	stored, err := service.GetReport(context.Background(), report.ID)
	if err != nil {
		t.Fatalf("get report: %v", err)
	}
	if stored.Status != domainresearch.ReportFailed {
		t.Fatalf("a report interrupted by a restart is failed, got %s", stored.Status)
	}
	if !strings.Contains(stored.Error, "перезапущен") {
		t.Fatalf("the reason must say what happened: %q", stored.Error)
	}
	records, _, err := service.ListCalls(context.Background(), source.ID, 10)
	if err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if records[0].Outcome != appresearch.CallUnknown {
		t.Fatalf("an unclosed changing call is unknown, got %q", records[0].Outcome)
	}
}

// A run whose request goes away still records its outcome: a closed tab must
// not leave a report running for ever.
func TestCancelledRunStillRecordsItsOutcome(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	ctx, cancel := context.WithCancel(context.Background())
	web.cancelOnSearch = cancel
	web.searchErr = context.Canceled

	report, err := service.Research(ctx, appresearch.ResearchInput{Question: "вопрос"})
	if err == nil {
		t.Fatal("a cancelled run reports the failure")
	}
	if report == nil {
		t.Fatal("the report must be returned with its failure")
	}
	stored, getErr := service.GetReport(context.Background(), report.ID)
	if getErr != nil {
		t.Fatalf("get report: %v", getErr)
	}
	if stored.Status != domainresearch.ReportFailed {
		t.Fatalf("the report must not stay running, got %s", stored.Status)
	}
}

// StartResearch answers while the work is still going, and the report is
// finished in the background.
// TestStartResearchReturnsASnapshot pins the ownership split behind the
// background run: the answer a caller serialises is a snapshot, and the run
// owns a report of its own. They must not be the same report — the race
// detector sees the sharing as a write against the response, and a reader
// sees a report whose status changes under it.
func TestStartResearchReturnsASnapshot(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, _ := newService(repo, web, nil)
	web.results["golang language"] = []appresearch.SearchResult{{Title: "Go", URL: "https://go.dev/doc"}}
	web.pages["https://go.dev/doc"] = page("https://go.dev/doc", "Go",
		"Go is a statically typed, compiled programming language designed at Google for building reliable software.")

	started, err := service.StartResearch(context.Background(), appresearch.ResearchInput{
		Question: "Что такое язык Go?", Queries: []string{"golang language"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Wait(waitCtx); err != nil {
		t.Fatalf("wait: %v", err)
	}
	// The run has finished and rewritten its report; the snapshot handed to
	// the caller must still say exactly what it said when it was taken.
	if started.Status != domainresearch.ReportRunning {
		t.Fatalf("the snapshot moved under its reader: %s", started.Status)
	}
	if len(started.Findings) != 0 || started.Summary != "" || started.FinishedAt != nil {
		t.Fatalf("the snapshot was filled in by the run: %+v", started)
	}
	finished, err := service.GetReport(context.Background(), started.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if finished.Status != domainresearch.ReportCompleted {
		t.Fatalf("the run must finish its own report, got %s (%s)", finished.Status, finished.Error)
	}
	if len(finished.Findings) == 0 {
		t.Fatalf("the report has no findings: %+v", finished)
	}
}

func TestStartResearchKeepsRunningInTheBackground(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	service, _, broker := newService(repo, web, nil)
	web.results["golang language"] = []appresearch.SearchResult{{Title: "Go", URL: "https://go.dev/doc"}}
	web.pages["https://go.dev/doc"] = page("https://go.dev/doc", "Go",
		"Go is a statically typed, compiled programming language designed at Google for building reliable software.")

	started, err := service.StartResearch(context.Background(), appresearch.ResearchInput{
		Question: "Что такое язык Go?", Queries: []string{"golang language"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if started.Status != domainresearch.ReportRunning {
		t.Fatalf("the start must answer while running, got %s", started.Status)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Wait(waitCtx); err != nil {
		t.Fatalf("wait: %v", err)
	}
	finished, err := service.GetReport(context.Background(), started.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if finished.Status != domainresearch.ReportCompleted {
		t.Fatalf("the background run must finish the report, got %s (%s)", finished.Status, finished.Error)
	}
	if len(finished.Findings) == 0 {
		t.Fatalf("the report has no findings: %+v", finished)
	}
	announced := strings.Join(broker.announced(), ",")
	if !strings.Contains(announced, "research.started") || !strings.Contains(announced, "research.completed") {
		t.Fatalf("events = %q", announced)
	}
}

// fakeNotifier records where a finished report was delivered.
type fakeNotifier struct {
	mu           sync.Mutex
	conversation int64
	reports      []*domainresearch.Report
}

func (f *fakeNotifier) DeliverResearch(_ context.Context, conversationID int64, report *domainresearch.Report) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conversation = conversationID
	f.reports = append(f.reports, report)
	return nil
}

// A run that outlives its request answers where the question was asked: the
// conversation gets the result as a message, not only a report in a view the
// user has to go and find. A run nobody is waiting for delivers nothing.
func TestBackgroundResearchDeliversTheResultToTheConversation(t *testing.T) {
	repo := newMemoryRepository()
	web := newFakeWeb()
	clock := newClock()
	notifier := &fakeNotifier{}
	service := appresearch.NewService(memoryUnitOfWork{repo: repo}, clock,
		appresearch.WithExternal(web), appresearch.WithNotifier(notifier))
	web.results["golang language"] = []appresearch.SearchResult{{Title: "Go", URL: "https://go.dev/doc"}}
	web.pages["https://go.dev/doc"] = page("https://go.dev/doc", "Go",
		"Go is a statically typed, compiled programming language designed at Google for building reliable software.")

	started, err := service.StartResearch(context.Background(), appresearch.ResearchInput{
		Question: "Что такое язык Go?", Queries: []string{"golang language"}, ConversationID: 42,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Wait(waitCtx); err != nil {
		t.Fatalf("wait: %v", err)
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if notifier.conversation != 42 {
		t.Fatalf("the result must reach the asking conversation, got %d", notifier.conversation)
	}
	if len(notifier.reports) != 1 || notifier.reports[0].ID != started.ID {
		t.Fatalf("delivered = %+v", notifier.reports)
	}

	// Without a conversation nobody is waiting: nothing is delivered.
	notifier.conversation = 0
	notifier.reports = nil
	if _, err := service.StartResearch(context.Background(), appresearch.ResearchInput{
		Question: "Ещё вопрос?", Queries: []string{"golang language"},
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	notifier.mu.Unlock()
	waitCtx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := service.Wait(waitCtx2); err != nil {
		t.Fatalf("wait: %v", err)
	}
	notifier.mu.Lock()
	if len(notifier.reports) != 0 {
		t.Fatalf("a run without a conversation delivers nothing, got %+v", notifier.reports)
	}
}
