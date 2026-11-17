// Roadmap stage 7: researcher and external sources.
//
// The service gathers evidence instead of asserting answers: it searches,
// reads the pages, keeps the passage that answers the question, and compares
// what independent sites say. A claim supported by one site stays an
// assumption; a claim two sites contradict is reported as a disagreement and
// never becomes a fact. Results go into the knowledge base with their sources,
// and a watch re-asks a question on a cadence and reports what changed.
package research

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
	"github.com/theDenuvo/LifeOS/internal/events"
)

const (
	defaultLimit    = 30
	maxLimit        = 100
	defaultMaxPages = 4
	maxPages        = 8
	// resultsPerQuery keeps one query from filling the whole reading list.
	resultsPerQuery  = 6
	maxQueriesPerRun = maxQueries
	// maxSnippetRunes bounds a search snippet used as a fallback claim.
	maxSnippetRunes = 400
)

// Service coordinates research runs, external sources and standing watches.
type Service struct {
	uow       UnitOfWork
	clock     Clock
	external  External
	knowledge KnowledgeSink
	broker    events.Publisher
}

// Option customises the service.
type Option func(*Service)

// WithExternal attaches the web transport: the built-in provider and the
// connected sources.
func WithExternal(external External) Option {
	return func(s *Service) { s.external = external }
}

// WithKnowledge attaches the knowledge base research results are saved to.
func WithKnowledge(sink KnowledgeSink) Option {
	return func(s *Service) { s.knowledge = sink }
}

// WithPublisher publishes research changes to the live stream.
func WithPublisher(publisher events.Publisher) Option {
	return func(s *Service) { s.broker = publisher }
}

// NewService builds the research service.
func NewService(uow UnitOfWork, clock Clock, opts ...Option) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	s := &Service{uow: uow, clock: clock}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// ─── Research runs ──────────────────────────────────────────────────────────

// Research investigates one question and stores the report. The evidence
// gathering happens outside the database transaction: a slow page must not
// hold the single writer of SQLite.
func (s *Service) Research(ctx context.Context, input ResearchInput) (*domainresearch.Report, error) {
	question := strings.TrimSpace(input.Question)
	queries := normalizeQueries(question, input.Queries)
	if len(queries) == 0 {
		return nil, ErrEmptyQuestion
	}
	if question == "" {
		question = queries[0]
	}
	if len([]rune(question)) > 500 {
		question = truncateRunes(question, 500)
	}
	if s.external == nil {
		return nil, ErrNoProvider
	}

	now := s.clock.Now().UTC()
	report := &domainresearch.Report{
		Question:  question,
		Queries:   queries,
		Status:    domainresearch.ReportRunning,
		StartedAt: now,
		Version:   1,
	}
	if err := report.Validate(); err != nil {
		return nil, err
	}
	var id int64
	if err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		id, err = tx.Research().InsertReport(ctx, report)
		return err
	}); err != nil {
		return nil, err
	}
	report.ID = id

	collected, runErr := s.collect(ctx, queries, input.ConnectionID, input.MaxPages)
	finished := s.clock.Now().UTC()
	report.FinishedAt = &finished
	if runErr != nil {
		report.Status = domainresearch.ReportFailed
		report.Error = ensureValidRunes(runErr.Error())
		report.Summary = fmt.Sprintf("## %s\n\nИсследование не выполнено: %s", question, report.Error)
	} else {
		report.Status = domainresearch.ReportCompleted
		report.Findings = collected.findings
		report.Sources = countSources(collected.findings)
		report.Summary = buildSummary(question, queries, collected.findings, len(collected.pages), finished)
	}

	// The knowledge entry is written before the report is finalised, so one
	// update records the evidence and its destination together. A knowledge
	// failure is recorded on the report instead of being hidden: the research
	// still happened, and pretending it was stored would be worse.
	if input.SaveKnowledge && len(report.Findings) > 0 && runErr == nil {
		entryID, err := s.saveKnowledge(ctx, report, input.KnowledgeTitle)
		if err != nil {
			slog.Error("research: saving the result to the knowledge base failed", "report_id", id, "error", err)
			report.Error = ensureValidRunes("результат не сохранён в базу знаний: " + err.Error())
		} else {
			report.KnowledgeEntryID = &entryID
		}
	}

	var saved *domainresearch.Report
	if err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		saved, err = tx.Research().UpdateReport(ctx, id, report.Version, report)
		return err
	}); err != nil {
		return nil, err
	}
	report.Version = saved.Version

	if runErr != nil {
		s.publish("research.failed", map[string]any{"id": id, "question": report.Question})
		return report, runErr
	}
	s.publish("research.completed", map[string]any{
		"id": id, "question": report.Question,
		"findings": len(report.Findings), "sources": report.Sources,
		"knowledge_id": report.KnowledgeEntryID,
	})
	return report, nil
}

// saveKnowledge writes the report as reusable knowledge with its provenance.
func (s *Service) saveKnowledge(ctx context.Context, report *domainresearch.Report, title string) (int64, error) {
	if s.knowledge == nil {
		return 0, ErrKnowledgeNotSaved
	}
	theTitle := strings.TrimSpace(title)
	if theTitle == "" {
		theTitle = "Исследование: " + report.Question
	}
	if len([]rune(theTitle)) > 200 {
		theTitle = truncateRunes(theTitle, 200)
	}
	sites := sourceSites(report.Findings)
	label := "Веб-источники"
	if len(sites) > 0 {
		label = "Веб: " + strings.Join(sites, ", ")
	}
	excerpt := ""
	if len(report.Findings) > 0 {
		excerpt = report.Findings[0].Statement
	}
	entryID, err := s.knowledge.SaveResearch(ctx, KnowledgeInput{
		Title:         theTitle,
		Content:       report.Summary,
		Certainty:     knowledgeCertainty(report.Findings),
		Confidence:    reportConfidence(report.Findings),
		SourceLabel:   truncateRunes(label, 500),
		SourceURL:     firstSourceURL(report.Findings),
		SourceExcerpt: truncateRunes(excerpt, 1000),
	})
	if err != nil {
		return 0, err
	}
	return entryID, nil
}

func reportConfidence(findings []domainresearch.Finding) float64 {
	if len(findings) == 0 {
		return 0
	}
	best := 0.0
	for _, finding := range findings {
		if finding.Confidence > best {
			best = finding.Confidence
		}
	}
	return best
}

func firstSourceURL(findings []domainresearch.Finding) string {
	for _, finding := range findings {
		if len(finding.Citations) > 0 {
			return finding.Citations[0].URL
		}
	}
	return ""
}

// collected is what one evidence-gathering pass produced.
type collected struct {
	findings []domainresearch.Finding
	pages    []domainresearch.Fingerprint
}

// collect searches, reads and compares. A failing page is skipped rather than
// failing the run: partial evidence with honest sources is the point.
func (s *Service) collect(ctx context.Context, queries []string, connectionID int64, pageBudget int) (collected, error) {
	if len(queries) > maxQueriesPerRun {
		queries = queries[:maxQueriesPerRun]
	}
	if pageBudget <= 0 {
		pageBudget = defaultMaxPages
	}
	if pageBudget > maxPages {
		pageBudget = maxPages
	}

	var (
		connection *domainresearch.Connection
		trust      = domainresearch.TrustNormal
	)
	if connectionID > 0 {
		found, err := s.GetConnection(ctx, connectionID)
		if err != nil {
			return collected{}, err
		}
		if !found.Enabled {
			return collected{}, fmt.Errorf("%w: %s", ErrSourceDisabled, found.Name)
		}
		if found.Kind != domainresearch.KindWebSearch {
			return collected{}, fmt.Errorf("источник %q не является поисковым: выберите источник типа web_search", found.Name)
		}
		connection = found
		trust = found.Trust
	}

	type target struct {
		result SearchResult
		tokens []string
	}
	order := make([]string, 0, resultsPerQuery*len(queries))
	byURL := map[string]target{}
	for _, query := range queries {
		results, err := s.external.Search(ctx, connection, query, resultsPerQuery)
		if err != nil {
			if len(byURL) == 0 {
				return collected{}, err
			}
			// One query failing must not erase what another one found.
			slog.Warn("research: search query failed", "query", query, "error", err)
			continue
		}
		tokens := domainresearch.Tokens(query)
		for _, result := range results {
			canonical := domainresearch.CanonicalURL(result.URL)
			if canonical == "" {
				continue
			}
			result.URL = canonical
			if _, exists := byURL[canonical]; exists {
				continue
			}
			byURL[canonical] = target{result: result, tokens: tokens}
			order = append(order, canonical)
		}
	}

	result := collected{pages: make([]domainresearch.Fingerprint, 0, pageBudget)}
	if len(order) == 0 {
		return result, nil
	}
	if len(order) > pageBudget {
		order = order[:pageBudget]
	}
	now := s.clock.Now().UTC()
	candidates := make([]domainresearch.Candidate, 0, len(order))
	for _, url := range order {
		item := byURL[url]
		page, err := s.external.Fetch(ctx, url)
		if err != nil {
			// The page did not open. Its search snippet is still a lead, and it
			// is labelled as one: low trust, excerpt quoted, no claim of having
			// read the page.
			slog.Warn("research: page fetch failed", "url", url, "error", err)
			if snippet := strings.TrimSpace(item.result.Snippet); len([]rune(snippet)) >= minPassageRunes {
				for _, claim := range snippetClaims(collapseSpaces(snippet), item.tokens) {
					candidates = append(candidates, domainresearch.Candidate{
						Statement: claim,
						Citation: domainresearch.Citation{
							URL:          url,
							Title:        item.result.Title,
							Site:         domainresearch.Host(url),
							Excerpt:      truncateRunes("Поисковая выдержка, страница не прочитана: "+claim, 500),
							FetchedAt:    now,
							Trust:        domainresearch.TrustLow,
							ConnectionID: connectionID,
						},
					})
				}
			}
			continue
		}
		if strings.TrimSpace(page.URL) != "" {
			page.URL = domainresearch.CanonicalURL(page.URL)
		}
		if page.URL == "" {
			page.URL = url
		}
		page.FetchedAt = now
		result.pages = append(result.pages, fingerprintOf(page, now))

		title := strings.TrimSpace(page.Title)
		if title == "" {
			title = item.result.Title
		}
		// Each sentence of the answering passage is a separate claim, so a
		// fact can be confirmed by a differently worded sentence on another
		// site instead of requiring identical paragraphs.
		for _, claim := range pageClaims(page.Text, item.tokens) {
			candidates = append(candidates, domainresearch.Candidate{
				Statement: claim,
				Citation: domainresearch.Citation{
					URL:          page.URL,
					Title:        truncateRunes(title, 300),
					Site:         domainresearch.Host(page.URL),
					Excerpt:      truncateRunes(claim, 500),
					FetchedAt:    now,
					Trust:        trust,
					ConnectionID: connectionID,
				},
			})
		}
	}

	result.findings = domainresearch.BuildFindings(candidates)
	return result, nil
}

func collapseSpaces(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// Search asks the built-in provider (or one connected search source) for
// pages, without reading them. The assistant uses it to decide what to open.
func (s *Service) Search(ctx context.Context, query string, limit int, connectionID int64) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, ErrEmptyQuestion
	}
	if s.external == nil {
		return nil, ErrNoProvider
	}
	if limit <= 0 || limit > 20 {
		limit = resultsPerQuery
	}
	var connection *domainresearch.Connection
	if connectionID > 0 {
		found, err := s.GetConnection(ctx, connectionID)
		if err != nil {
			return nil, err
		}
		if !found.Enabled {
			return nil, fmt.Errorf("%w: %s", ErrSourceDisabled, found.Name)
		}
		if found.Kind != domainresearch.KindWebSearch {
			return nil, fmt.Errorf("источник %q не является поисковым", found.Name)
		}
		connection = found
	}
	return s.external.Search(ctx, connection, strings.TrimSpace(query), limit)
}

// Fetch reads one page. It is how the assistant checks a source the user
// named.
func (s *Service) Fetch(ctx context.Context, rawURL string) (Page, error) {
	if s.external == nil {
		return Page{}, ErrNoProvider
	}
	page, err := s.external.Fetch(ctx, rawURL)
	if err != nil {
		return Page{}, err
	}
	page.FetchedAt = s.clock.Now().UTC()
	return page, nil
}

// GetReport returns one stored report.
func (s *Service) GetReport(ctx context.Context, id int64) (*domainresearch.Report, error) {
	var report *domainresearch.Report
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		report, err = tx.Research().GetReport(ctx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// ListReports returns a page of stored research.
func (s *Service) ListReports(ctx context.Context, filter ReportFilter) ([]*domainresearch.Report, int, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > maxLimit {
		filter.Limit = maxLimit
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	var (
		reports []*domainresearch.Report
		total   int
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		reports, total, err = tx.Research().ListReports(ctx, filter)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return reports, total, nil
}

// DeleteReport removes a report. The knowledge entry it produced stays: what
// was learned is not unlearned by tidying the research list.
func (s *Service) DeleteReport(ctx context.Context, id int64) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Research().DeleteReport(ctx, id)
	})
	if err != nil {
		return err
	}
	s.publish("research.deleted", map[string]any{"id": id})
	return nil
}

// ─── External sources ───────────────────────────────────────────────────────

// CreateConnection registers an external source. An assistant-created source
// starts disabled and can only read: enabling it and granting writes is the
// user's decision.
func (s *Service) CreateConnection(ctx context.Context, connection *domainresearch.Connection) (*domainresearch.Connection, error) {
	if connection.Kind == domainresearch.KindScript && connection.CreatedByAssistant {
		return nil, ErrScriptNeedsUser
	}
	if connection.CreatedByAssistant {
		connection.Enabled = false
		if connection.Mode != domainresearch.ModeRead {
			return nil, fmt.Errorf("%w: ассистент подключает источник только на чтение", ErrSourceReadOnly)
		}
	}
	now := s.clock.Now().UTC()
	connection.Name = strings.TrimSpace(connection.Name)
	connection.Endpoint = strings.TrimSpace(connection.Endpoint)
	connection.Notes = strings.TrimSpace(connection.Notes)
	connection.CreatedAt = now
	connection.UpdatedAt = now
	connection.Version = 1
	if err := connection.Validate(); err != nil {
		return nil, err
	}
	var id int64
	if err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		id, err = tx.Research().InsertConnection(ctx, connection)
		return err
	}); err != nil {
		return nil, err
	}
	connection.ID = id
	s.publish("research.source.created", map[string]any{"id": id, "name": connection.Name, "enabled": connection.Enabled})
	return connection, nil
}

// GetConnection returns one source.
func (s *Service) GetConnection(ctx context.Context, id int64) (*domainresearch.Connection, error) {
	var connection *domainresearch.Connection
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		connection, err = tx.Research().GetConnection(ctx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return connection, nil
}

// ListConnections returns every source the user connected.
func (s *Service) ListConnections(ctx context.Context, filter ConnectionFilter) ([]*domainresearch.Connection, error) {
	var found []*domainresearch.Connection
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		found, err = tx.Research().ListConnections(ctx, filter)
		return err
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// ConnectionUpdate is a partial edit of a source. Grants (mode, endpoint,
// enabling) are changed here and only here: the assistant does not widen its
// own access.
type ConnectionUpdate struct {
	Name     *string
	Notes    *string
	Trust    *domainresearch.Trust
	Mode     *domainresearch.AccessMode
	Endpoint *string
	Format   *domainresearch.SearchFormat
	Enabled  *bool
}

// UpdateConnection applies a partial edit under the optimistic version.
func (s *Service) UpdateConnection(ctx context.Context, id int64, expectedVersion int, update ConnectionUpdate) (*domainresearch.Connection, error) {
	now := s.clock.Now().UTC()
	var updated *domainresearch.Connection
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Research()
		current, err := repo.GetConnection(ctx, id)
		if err != nil {
			return err
		}
		next := *current
		if update.Name != nil {
			next.Name = strings.TrimSpace(*update.Name)
		}
		if update.Notes != nil {
			next.Notes = strings.TrimSpace(*update.Notes)
		}
		if update.Trust != nil {
			next.Trust = *update.Trust
		}
		if update.Mode != nil {
			next.Mode = *update.Mode
		}
		if update.Endpoint != nil {
			next.Endpoint = strings.TrimSpace(*update.Endpoint)
		}
		if update.Format != nil {
			next.Format = *update.Format
		}
		if update.Enabled != nil {
			next.Enabled = *update.Enabled
		}
		next.UpdatedAt = now
		if err := next.Validate(); err != nil {
			return err
		}
		updated, err = repo.UpdateConnection(ctx, id, expectedVersion, &next)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("research.source.updated", map[string]any{"id": id, "enabled": updated.Enabled, "mode": string(updated.Mode)})
	return updated, nil
}

// SetConnectionEnabled enables or disables a source.
func (s *Service) SetConnectionEnabled(ctx context.Context, id int64, enabled bool) (*domainresearch.Connection, error) {
	current, err := s.GetConnection(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.UpdateConnection(ctx, id, current.Version, ConnectionUpdate{Enabled: &enabled})
}

// DeleteConnection removes a source together with its audit journal link; the
// journal rows are kept by the caller's choice of cascade in the schema.
func (s *Service) DeleteConnection(ctx context.Context, id int64) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Research().DeleteConnection(ctx, id)
	})
	if err != nil {
		return err
	}
	s.publish("research.source.deleted", map[string]any{"id": id})
	return nil
}

// CallSource performs one interaction with a connected source and journals
// it. A read-only source refuses anything that would change the service.
func (s *Service) CallSource(ctx context.Context, id int64, request CallRequest) (*CallResult, error) {
	connection, err := s.GetConnection(ctx, id)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = "GET"
	}
	operation := strings.TrimSpace(request.Operation)
	if len([]rune(operation)) > 500 || len(request.Body) > 20000 {
		return nil, ErrInvalidCall
	}
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
	default:
		return nil, ErrInvalidCall
	}
	write := request.Write || (method != "GET" && method != "HEAD")
	if !connection.Enabled {
		s.auditCall(ctx, connection, operation, CallRefused, "источник отключён")
		return nil, fmt.Errorf("%w: %s", ErrSourceDisabled, connection.Name)
	}
	if write && !connection.Mode.AllowsWrite() {
		s.auditCall(ctx, connection, operation, CallRefused, "источнику разрешено только чтение")
		return nil, fmt.Errorf("%w: %s", ErrSourceReadOnly, connection.Name)
	}
	request.Operation = operation
	request.Method = method
	request.Write = write

	started := time.Now()
	result, err := s.external.Call(ctx, connection, request)
	if err != nil {
		s.auditCall(ctx, connection, operation, CallFailed, err.Error())
		return nil, err
	}
	result.Duration = time.Since(started)
	s.auditCall(ctx, connection, operation, CallOK, callDetail(result))
	s.touchConnection(ctx, connection)
	return &result, nil
}

func callDetail(result CallResult) string {
	body := collapseSpaces(result.Body)
	if len([]rune(body)) > 300 {
		body = truncateRunes(body, 300)
	}
	return fmt.Sprintf("%d %s", result.Status, body)
}

// auditCall writes the journal entry. A failed audit write is logged, never
// returned: the interaction already happened, and pretending otherwise would
// hide it.
func (s *Service) auditCall(ctx context.Context, connection *domainresearch.Connection, operation, outcome, detail string) {
	record := &CallRecord{
		ConnectionID: connection.ID,
		Name:         connection.Name,
		Kind:         connection.Kind,
		Mode:         connection.Mode,
		Operation:    truncateRunes(operation, 200),
		Outcome:      outcome,
		Detail:       truncateRunes(ensureValidRunes(detail), 500),
		CreatedAt:    s.clock.Now().UTC(),
	}
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Research().RecordCall(ctx, record)
	})
	if err != nil {
		slog.Error("research: external call journal write failed", "source_id", connection.ID, "error", err)
	}
}

func (s *Service) touchConnection(ctx context.Context, connection *domainresearch.Connection) {
	now := s.clock.Now().UTC()
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		current, err := tx.Research().GetConnection(ctx, connection.ID)
		if err != nil {
			return err
		}
		current.LastUsedAt = &now
		current.UpdatedAt = now
		_, err = tx.Research().UpdateConnection(ctx, connection.ID, current.Version, current)
		return err
	})
	if err != nil {
		slog.Warn("research: recording the last use of a source failed", "source_id", connection.ID, "error", err)
	}
}

// ListCalls returns the audit journal of one source.
func (s *Service) ListCalls(ctx context.Context, connectionID int64, limit int) ([]*CallRecord, int, error) {
	if limit <= 0 || limit > maxLimit {
		limit = 20
	}
	var (
		records []*CallRecord
		total   int
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		records, total, err = tx.Research().ListCalls(ctx, connectionID, limit)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

// ─── Watches ────────────────────────────────────────────────────────────────

// CreateWatch starts watching a topic. The first check runs immediately when
// the watch is enabled, so the baseline is the state at creation rather than a
// surprise change on the first tick.
func (s *Service) CreateWatch(ctx context.Context, input WatchInput) (*domainresearch.Watch, error) {
	now := s.clock.Now().UTC()
	watch := &domainresearch.Watch{
		Topic:     strings.TrimSpace(input.Topic),
		Query:     strings.TrimSpace(input.Query),
		Queries:   normalizeQueries(input.Query, input.Queries),
		Cadence:   input.Cadence,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}
	if watch.Cadence == "" {
		watch.Cadence = domainresearch.CadenceDaily
	}
	if input.Enabled != nil {
		watch.Enabled = *input.Enabled
	}
	if watch.Query == "" && len(watch.Queries) > 0 {
		watch.Query = watch.Queries[0]
	}
	if err := watch.Validate(); err != nil {
		return nil, err
	}
	watch.NextCheckAt = watch.NextCheck(now)
	var id int64
	if err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		id, err = tx.Research().InsertWatch(ctx, watch)
		return err
	}); err != nil {
		return nil, err
	}
	watch.ID = id
	s.publish("research.watch.created", map[string]any{"id": id, "topic": watch.Topic})
	return watch, nil
}

// GetWatch returns one watch.
func (s *Service) GetWatch(ctx context.Context, id int64) (*domainresearch.Watch, error) {
	var watch *domainresearch.Watch
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		watch, err = tx.Research().GetWatch(ctx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return watch, nil
}

// ListWatches returns the standing questions.
func (s *Service) ListWatches(ctx context.Context, includeDisabled bool) ([]*domainresearch.Watch, error) {
	var watches []*domainresearch.Watch
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		watches, err = tx.Research().ListWatches(ctx, includeDisabled)
		return err
	})
	if err != nil {
		return nil, err
	}
	return watches, nil
}

// UpdateWatch edits a watch.
func (s *Service) UpdateWatch(ctx context.Context, id int64, expectedVersion int, update WatchUpdate) (*domainresearch.Watch, error) {
	now := s.clock.Now().UTC()
	var updated *domainresearch.Watch
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Research()
		current, err := repo.GetWatch(ctx, id)
		if err != nil {
			return err
		}
		next := *current
		if update.Topic != nil {
			next.Topic = strings.TrimSpace(*update.Topic)
		}
		if update.Query != nil {
			next.Query = strings.TrimSpace(*update.Query)
			next.Queries = normalizeQueries(next.Query, nil)
		}
		if update.Queries != nil {
			next.Queries = normalizeQueries(next.Query, update.Queries)
		}
		if update.Cadence != nil {
			next.Cadence = *update.Cadence
		}
		if update.Enabled != nil {
			next.Enabled = *update.Enabled
		}
		next.UpdatedAt = now
		next.NextCheckAt = next.NextCheck(now)
		if err := next.Validate(); err != nil {
			return err
		}
		updated, err = repo.UpdateWatch(ctx, id, expectedVersion, &next)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish("research.watch.updated", map[string]any{"id": id, "enabled": updated.Enabled})
	return updated, nil
}

// DeleteWatch stops watching a topic.
func (s *Service) DeleteWatch(ctx context.Context, id int64) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		return tx.Research().DeleteWatch(ctx, id)
	})
	if err != nil {
		return err
	}
	s.publish("research.watch.deleted", map[string]any{"id": id})
	return nil
}

// CheckWatch re-runs one watch now and reports what changed. It is both the
// manual "проверить сейчас" action and the body of the periodic pass.
func (s *Service) CheckWatch(ctx context.Context, id int64) (*domainresearch.Watch, *domainresearch.WatchDiff, error) {
	watch, err := s.GetWatch(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	queries := watch.Queries
	if len(queries) == 0 {
		queries = normalizeQueries(watch.Query, nil)
	}
	collected, runErr := s.collect(ctx, queries, 0, defaultMaxPages)
	now := s.clock.Now().UTC()
	diff := domainresearch.WatchDiff{}

	var updated *domainresearch.Watch
	err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		repo := tx.Research()
		current, err := repo.GetWatch(ctx, id)
		if err != nil {
			return err
		}
		next := *current
		next.LastCheckedAt = &now
		next.UpdatedAt = now
		next.NextCheckAt = next.NextCheck(now)
		if runErr == nil {
			var merged []domainresearch.Fingerprint
			diff, merged = next.Diff(collected.pages)
			next.Seen = merged
			if diff.Changed() {
				next.LastChangeAt = &now
				next.LastChangeSummary = diff.Summary()
			}
		}
		updated, err = repo.UpdateWatch(ctx, id, current.Version, &next)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	if runErr != nil {
		return updated, nil, runErr
	}
	if diff.Changed() {
		s.publish("research.watch.changed", map[string]any{
			"id": id, "topic": updated.Topic,
			"new": len(diff.New), "updated": len(diff.Updated),
			"summary": diff.Summary(),
		})
	} else {
		s.publish("research.watch.checked", map[string]any{"id": id, "topic": updated.Topic})
	}
	return updated, &diff, nil
}

// CheckDue re-checks the watches whose moment has come. One failing watch does
// not stop the pass.
func (s *Service) CheckDue(ctx context.Context, now time.Time) WatchTick {
	tick := WatchTick{}
	var due []*domainresearch.Watch
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		var err error
		due, err = tx.Research().DueWatches(ctx, now)
		return err
	})
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("research: watch query failed", "error", err)
		}
		return tick
	}
	for _, watch := range due {
		if ctx.Err() != nil {
			return tick
		}
		watch, diff, err := s.CheckWatch(ctx, watch.ID)
		if err != nil {
			tick.Failed++
			if ctx.Err() == nil {
				slog.Warn("research: watch check failed", "watch_id", watch.ID, "error", err)
			}
			continue
		}
		_ = watch
		tick.Checked++
		if diff != nil && diff.Changed() {
			tick.Changed++
		}
	}
	return tick
}

// Change is one watch whose answer moved.
type Change struct {
	WatchID   int64
	Topic     string
	Summary   string
	ChangedAt time.Time
}

// RecentChanges returns the watches that reported a change after `since`. It is
// what feeds the proactive insight about a topic the user asked LifeOS to
// follow; the report itself stays the place where the evidence lives.
func (s *Service) RecentChanges(ctx context.Context, since time.Time) ([]Change, error) {
	watches, err := s.ListWatches(ctx, true)
	if err != nil {
		return nil, err
	}
	changes := make([]Change, 0, len(watches))
	for _, watch := range watches {
		if watch.LastChangeAt == nil || watch.LastChangeAt.Before(since) {
			continue
		}
		changes = append(changes, Change{
			WatchID:   watch.ID,
			Topic:     watch.Topic,
			Summary:   watch.LastChangeSummary,
			ChangedAt: *watch.LastChangeAt,
		})
	}
	return changes, nil
}

func (s *Service) publish(eventType string, payload map[string]any) {
	if s.broker == nil {
		return
	}
	if err := s.broker.Publish(eventType, payload); err != nil {
		// Durable state is already written; a lost live event only delays the
		// interface refresh.
		slog.Error("publish research event failed", "event", eventType, "error", err)
	}
}
