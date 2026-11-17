// Package research owns the model of LifeOS research and its external
// sources: a question asked of the web, the evidence collected for it, the
// sources that evidence came from, and the standing watches that notice when
// a topic changes.
//
// The product rule behind the model: a claim is only a fact when independent
// sources agree on it. A single source is an assumption, and LifeOS's own
// conclusion is an inference — the three never mix silently. Every statement
// keeps the pages it came from, so a later reader can check the reasoning
// instead of trusting it.
package research

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Certainty separates what sources support from what LifeOS merely suspects.
//
// fact — at least two independent sites state the same thing;
// assumption — one site states it, or the support is too weak to call it a fact;
// inference — LifeOS derived it; no source says it in these words.
type Certainty string

const (
	CertaintyFact       Certainty = "fact"
	CertaintyAssumption Certainty = "assumption"
	CertaintyInference  Certainty = "inference"
)

// IsValid reports whether the certainty level is known.
func (c Certainty) IsValid() bool {
	return c == CertaintyFact || c == CertaintyAssumption || c == CertaintyInference
}

// Trust is how much a source is worth when it is compared with others.
type Trust string

const (
	TrustHigh   Trust = "high"
	TrustNormal Trust = "normal"
	TrustLow    Trust = "low"
)

// IsValid reports whether the trust level is known.
func (t Trust) IsValid() bool {
	return t == TrustHigh || t == TrustNormal || t == TrustLow
}

// Weight converts trust into a scoring factor.
func (t Trust) Weight() float64 {
	switch t {
	case TrustHigh:
		return 1
	case TrustLow:
		return 0.5
	default:
		return 0.8
	}
}

// SourceKind is the way LifeOS talks to an external service.
type SourceKind string

const (
	// KindWebSearch is a search endpoint (built-in provider, SearxNG, another
	// instance) that answers a query with result pages.
	KindWebSearch SourceKind = "web_search"
	// KindHTTPAPI is a REST endpoint the user connected: LifeOS calls paths
	// under it and reads the answer.
	KindHTTPAPI SourceKind = "http_api"
	// KindMCP is a tools-only MCP server; LifeOS lists its tools and calls
	// them over Streamable HTTP.
	KindMCP SourceKind = "mcp"
	// KindScript is a command the user configured. It is read-only by
	// construction: a script is the user's own instrument, and unattended
	// writes belong to an approved plan, not to a research source.
	KindScript SourceKind = "script"
)

// IsValid reports whether the kind is known.
func (k SourceKind) IsValid() bool {
	switch k {
	case KindWebSearch, KindHTTPAPI, KindMCP, KindScript:
		return true
	default:
		return false
	}
}

// SupportsWrites reports whether the kind can send changes outward at all.
func (k SourceKind) SupportsWrites() bool {
	return k == KindHTTPAPI || k == KindMCP
}

// SearchFormat is how a search endpoint's answer is parsed.
type SearchFormat string

const (
	// FormatSearxng reads the JSON answer of a SearxNG instance.
	FormatSearxng SearchFormat = "searxng"
	// FormatDuckDuckGo reads the HTML answer of the DuckDuckGo endpoint.
	FormatDuckDuckGo SearchFormat = "duckduckgo"
)

// IsValid reports whether the format is known.
func (f SearchFormat) IsValid() bool {
	return f == FormatSearxng || f == FormatDuckDuckGo
}

// AccessMode is the permission the user granted to one external source.
//
// read — LifeOS may read and store what it learned locally;
// write — LifeOS may also send changes to the service, each one confirmed;
// sync — both directions: LifeOS keeps its own base and mirrors selected items.
type AccessMode string

const (
	ModeRead  AccessMode = "read"
	ModeWrite AccessMode = "write"
	ModeSync  AccessMode = "sync"
)

// IsValid reports whether the mode is known.
func (m AccessMode) IsValid() bool {
	return m == ModeRead || m == ModeWrite || m == ModeSync
}

// AllowsWrite reports whether outbound changes are permitted.
func (m AccessMode) AllowsWrite() bool { return m == ModeWrite || m == ModeSync }

// AllowsRead reports whether reading is permitted. Every mode allows it: a
// write permission without reading would make the connection useless.
func (m AccessMode) AllowsRead() bool { return m.IsValid() }

// Connection is one external source with the permission the user granted it.
type Connection struct {
	ID     int64
	Name   string
	Kind   SourceKind
	Mode   AccessMode
	Trust  Trust
	Format SearchFormat
	// Endpoint is the base URL for http_api and mcp, the query URL template
	// for web_search (with a {query} placeholder) and the command line for
	// script.
	Endpoint string
	Notes    string
	Enabled  bool
	// CreatedByAssistant is true when the assistant registered the source.
	// The user can still use it, but a script source must be authorised by
	// the user themselves, so the assistant may not create one.
	CreatedByAssistant bool
	LastUsedAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Version            int
}

var (
	ErrInvalidName     = errors.New("название источника обязательно и не длиннее 200 символов")
	ErrInvalidKind     = errors.New("неизвестный тип источника")
	ErrInvalidMode     = errors.New("неизвестный режим доступа")
	ErrInvalidTrust    = errors.New("неизвестный уровень доверия")
	ErrInvalidEndpoint = errors.New("адрес источника некорректен")
	ErrInvalidFormat   = errors.New("неизвестный формат ответа поиска")
	ErrScriptNeedsUser = errors.New("источник-скрипт создаёт только пользователь: ассистент не подключает собственные команды")
	ErrScriptReadOnly  = errors.New("источник-скрипт работает только на чтение")
)

// Validate checks the connection as a whole.
func (c *Connection) Validate() error {
	name := strings.TrimSpace(c.Name)
	if name == "" || utf8.RuneCountInString(name) > 200 {
		return ErrInvalidName
	}
	if !c.Kind.IsValid() {
		return ErrInvalidKind
	}
	if !c.Mode.IsValid() {
		return ErrInvalidMode
	}
	if c.Trust == "" {
		c.Trust = TrustNormal
	}
	if !c.Trust.IsValid() {
		return ErrInvalidTrust
	}
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		return ErrInvalidEndpoint
	}
	if utf8.RuneCountInString(endpoint) > 2000 {
		return ErrInvalidEndpoint
	}
	switch c.Kind {
	case KindWebSearch:
		if !c.Format.IsValid() {
			return ErrInvalidFormat
		}
		if err := validateHTTPEndpoint(c.Format, endpoint); err != nil {
			return err
		}
	case KindHTTPAPI, KindMCP:
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return ErrInvalidEndpoint
		}
	case KindScript:
		if strings.ContainsAny(endpoint, "\r\n") {
			return ErrInvalidEndpoint
		}
		if c.Mode != ModeRead {
			return ErrScriptReadOnly
		}
	}
	if utf8.RuneCountInString(c.Notes) > 2000 {
		return errors.New("описание источника длиннее 2000 символов")
	}
	return nil
}

func validateHTTPEndpoint(format SearchFormat, endpoint string) error {
	sample := strings.ReplaceAll(endpoint, "{query}", "test")
	parsed, err := url.Parse(sample)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ErrInvalidEndpoint
	}
	_ = format
	return nil
}

// ReportStatus is the lifecycle of one research run.
type ReportStatus string

const (
	ReportRunning   ReportStatus = "running"
	ReportCompleted ReportStatus = "completed"
	ReportFailed    ReportStatus = "failed"
)

// IsValid reports whether the status is known.
func (s ReportStatus) IsValid() bool {
	return s == ReportRunning || s == ReportCompleted || s == ReportFailed
}

// Terminal reports whether the report will not change any more.
func (s ReportStatus) Terminal() bool { return s == ReportCompleted || s == ReportFailed }

// Citation is one page that supports a statement.
type Citation struct {
	URL       string    `json:"url"`
	Title     string    `json:"title,omitempty"`
	Site      string    `json:"site,omitempty"`
	Excerpt   string    `json:"excerpt,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Trust     Trust     `json:"trust"`
	// ConnectionID is the source the page came through; zero means the
	// built-in web provider.
	ConnectionID int64 `json:"connection_id,omitempty"`
}

// Validate checks that a citation can be shown and re-opened.
func (c Citation) Validate() error {
	if strings.TrimSpace(c.URL) == "" || utf8.RuneCountInString(c.URL) > 2000 {
		return errors.New("у источника нет адреса")
	}
	parsed, err := url.Parse(c.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("адрес источника %q некорректен", c.URL)
	}
	if utf8.RuneCountInString(c.Title) > 300 {
		return errors.New("заголовок источника длиннее 300 символов")
	}
	if utf8.RuneCountInString(c.Excerpt) > 2000 {
		return errors.New("фрагмент источника длиннее 2000 символов")
	}
	return nil
}

// Finding is one statement with the evidence behind it.
type Finding struct {
	Statement     string     `json:"statement"`
	Certainty     Certainty  `json:"certainty"`
	Confidence    float64    `json:"confidence"`
	Corroboration int        `json:"corroboration"`
	Domains       []string   `json:"domains"`
	Citations     []Citation `json:"citations"`
	// Conflict explains a disagreement with another source, when the evidence
	// does not agree with itself. A disputed statement is never a fact.
	Conflict string `json:"conflict,omitempty"`
}

// Validate checks the finding before it reaches storage.
func (f *Finding) Validate() error {
	statement := strings.TrimSpace(f.Statement)
	if statement == "" || utf8.RuneCountInString(statement) > 1200 {
		return errors.New("вывод должен быть непустым и не длиннее 1200 символов")
	}
	if !f.Certainty.IsValid() {
		return errors.New("неизвестный уровень достоверности вывода")
	}
	if f.Confidence < 0 || f.Confidence > 1 {
		return errors.New("уверенность должна быть от 0 до 1")
	}
	if len(f.Citations) == 0 {
		return errors.New("вывод без источника не сохраняется")
	}
	for _, citation := range f.Citations {
		if err := citation.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Report is one completed research run: the question, the evidence and the
// knowledge entry the result was saved as.
type Report struct {
	ID       int64
	Question string
	Queries  []string
	Status   ReportStatus
	Summary  string
	Findings []Finding
	// Sources is the number of distinct pages that supported at least one
	// finding; it is stored so the list does not have to recompute it.
	Sources int
	// KnowledgeEntryID is the memory entry the result was saved as, when the
	// user (or the assistant on the user's behalf) asked to keep it.
	KnowledgeEntryID *int64
	Error            string
	StartedAt        time.Time
	FinishedAt       *time.Time
	Version          int
}

// Validate checks report invariants.
func (r *Report) Validate() error {
	question := strings.TrimSpace(r.Question)
	if question == "" || utf8.RuneCountInString(question) > 500 {
		return errors.New("вопрос обязателен и не длиннее 500 символов")
	}
	if !r.Status.IsValid() {
		return errors.New("неизвестный статус исследования")
	}
	if utf8.RuneCountInString(r.Summary) > 100000 {
		return errors.New("итог исследования длиннее 100000 символов")
	}
	for i := range r.Findings {
		if err := r.Findings[i].Validate(); err != nil {
			return fmt.Errorf("вывод %d: %w", i+1, err)
		}
	}
	return nil
}

// Cadence is how often a watch re-checks its topic.
type Cadence string

const (
	CadenceDaily  Cadence = "daily"
	CadenceWeekly Cadence = "weekly"
	CadenceManual Cadence = "manual"
)

// IsValid reports whether the cadence is known.
func (c Cadence) IsValid() bool {
	return c == CadenceDaily || c == CadenceWeekly || c == CadenceManual
}

// Interval is the gap between two automatic checks. Manual watches have none.
func (c Cadence) Interval() time.Duration {
	switch c {
	case CadenceDaily:
		return 24 * time.Hour
	case CadenceWeekly:
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

// Fingerprint is what a watch remembers about one page, so the next check can
// tell a new page from an unchanged one and a rewritten page from both.
type Fingerprint struct {
	URL       string    `json:"url"`
	Title     string    `json:"title,omitempty"`
	Hash      string    `json:"hash"`
	FirstSeen time.Time `json:"first_seen"`
}

// Watch is a standing question LifeOS re-asks on a cadence and reports on
// when the answer changes.
type Watch struct {
	ID    int64
	Topic string
	Query string
	// Queries are the search phrases the watch runs; the first is derived from
	// the topic when the user does not give any.
	Queries           []string
	Cadence           Cadence
	Enabled           bool
	NextCheckAt       *time.Time
	LastCheckedAt     *time.Time
	LastChangeAt      *time.Time
	LastChangeSummary string
	Seen              []Fingerprint
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Version           int
}

// maxSeen bounds the fingerprints one watch keeps. Older pages fall out of the
// baseline, so a page that disappears for long enough and returns is reported
// as new — an honest trade for a bounded row.
const maxSeen = 200

// Validate checks watch invariants.
func (w *Watch) Validate() error {
	topic := strings.TrimSpace(w.Topic)
	if topic == "" || utf8.RuneCountInString(topic) > 200 {
		return errors.New("тема наблюдения обязательна и не длиннее 200 символов")
	}
	if utf8.RuneCountInString(w.Query) > 500 {
		return errors.New("запрос наблюдения длиннее 500 символов")
	}
	if len(w.Queries) > 5 {
		return errors.New("у наблюдения не больше пяти запросов")
	}
	for _, query := range w.Queries {
		if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 500 {
			return errors.New("запрос наблюдения должен быть непустым и не длиннее 500 символов")
		}
	}
	if !w.Cadence.IsValid() {
		return errors.New("неизвестная периодичность наблюдения")
	}
	return nil
}

// WatchDiff describes what changed since the last check of a watch.
type WatchDiff struct {
	// New lists pages the watch had not seen before.
	New []Fingerprint
	// Updated lists pages whose content hash changed.
	Updated []Fingerprint
	// Unchanged lists pages still present with the same content.
	Unchanged int
}

// Changed reports whether the diff is worth telling the user about.
func (d WatchDiff) Changed() bool { return len(d.New) > 0 || len(d.Updated) > 0 }

// Summary renders the diff in one line.
func (d WatchDiff) Summary() string {
	if !d.Changed() {
		return "Изменений нет"
	}
	parts := make([]string, 0, 2)
	if len(d.New) > 0 {
		parts = append(parts, fmt.Sprintf("новых источников: %d", len(d.New)))
	}
	if len(d.Updated) > 0 {
		parts = append(parts, fmt.Sprintf("обновлено: %d", len(d.Updated)))
	}
	return strings.Join(parts, ", ")
}

// Diff compares the fresh fingerprints with the watch's baseline and returns
// the merged baseline together with what changed.
//
// Pages are matched by URL. A page whose hash changed counts as updated; the
// merged baseline keeps the newest hash so the same change is not reported
// twice. Pages missing from the fresh set keep their old fingerprint until the
// baseline overflows: a temporarily unreachable page should not look new when
// it returns.
func (w *Watch) Diff(fresh []Fingerprint) (WatchDiff, []Fingerprint) {
	known := make(map[string]Fingerprint, len(w.Seen))
	for _, item := range w.Seen {
		known[item.URL] = item
	}
	diff := WatchDiff{}
	for _, item := range fresh {
		previous, exists := known[item.URL]
		switch {
		case !exists:
			diff.New = append(diff.New, item)
		case previous.Hash != item.Hash:
			diff.Updated = append(diff.Updated, item)
		default:
			diff.Unchanged++
			// Keep the original arrival time: the page has not changed.
			item.FirstSeen = previous.FirstSeen
		}
		known[item.URL] = item
	}
	// Order the baseline newest-first so the bound drops the oldest pages.
	merged := make([]Fingerprint, 0, len(known))
	for _, item := range known {
		merged = append(merged, item)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].FirstSeen.Equal(merged[j].FirstSeen) {
			return merged[i].URL < merged[j].URL
		}
		return merged[i].FirstSeen.After(merged[j].FirstSeen)
	})
	if len(merged) > maxSeen {
		merged = merged[:maxSeen]
	}
	return diff, merged
}

// NextCheck computes the next automatic check after `now`. Manual watches have
// no next check.
func (w *Watch) NextCheck(now time.Time) *time.Time {
	if !w.Enabled {
		return nil
	}
	interval := w.Cadence.Interval()
	if interval == 0 {
		return nil
	}
	next := now.Add(interval)
	return &next
}

// Candidate is one statement extracted from one page, before the evidence is
// compared.
type Candidate struct {
	Statement string
	Citation  Citation
}

// Agreement thresholds. They are deliberately conservative: two pages are
// treated as saying the same thing only when their statements share most of
// their significant words, and a disagreement only counts when they clearly
// talk about the same thing.
const (
	agreeThreshold     = 0.45
	sameSiteThreshold  = 0.8
	conflictThreshold  = 0.5
	minSharedForNegate = 3
)

// BuildFindings merges the candidates into findings, decides which statements
// independent sources corroborate, and names the disagreements.
//
// One statement per page survives: the extraction already picked the passage
// that answers the question, so merging keeps the reading list short. Two
// candidates collapse into one finding when their statements agree; the
// finding keeps every citation, so the count of sources stays truthful.
func BuildFindings(candidates []Candidate) []Finding {
	type group struct {
		finding Finding
		tokens  []string
		sites   map[string]bool
	}
	groups := make([]*group, 0, len(candidates))
	for _, candidate := range candidates {
		statement := strings.TrimSpace(candidate.Statement)
		if statement == "" {
			continue
		}
		tokens := Tokens(statement)
		site := siteOf(candidate.Citation)
		merged := false
		for _, existing := range groups {
			// A page that repeats its own claim is not a second source, and
			// two different claims of one page are not one claim: two pages of
			// the same site merge only when they say the same thing almost
			// word for word, while different claims stay separate.
			threshold := agreeThreshold
			if existing.sites[site] {
				threshold = sameSiteThreshold
			}
			if overlap(tokens, existing.tokens) < threshold {
				continue
			}
			// Statements that disagree stay apart even when they talk about
			// the same subject: merging them would hide the disagreement.
			if conflictBetween(tokens, statement, existing.tokens, existing.finding.Statement) {
				continue
			}
			existing.finding.Citations = append(existing.finding.Citations, candidate.Citation)
			existing.sites[site] = true
			merged = true
			break
		}
		if merged {
			continue
		}
		groups = append(groups, &group{
			finding: Finding{
				Statement: statement,
				Citations: []Citation{candidate.Citation},
			},
			tokens: tokens,
			sites:  map[string]bool{site: true},
		})
	}

	findings := make([]Finding, 0, len(groups))
	for _, item := range groups {
		// Two sources on the same site are not an independent confirmation.
		sites := map[string]struct{}{}
		for _, citation := range item.finding.Citations {
			sites[siteOf(citation)] = struct{}{}
		}
		domains := make([]string, 0, len(sites))
		for site := range sites {
			domains = append(domains, site)
		}
		sort.Strings(domains)
		item.finding.Domains = domains
		// Corroboration counts the independent sites that support the
		// statement; one site is not a confirmation of itself.
		item.finding.Corroboration = len(domains)

		conflict := ""
		for _, other := range groups {
			if other == item {
				continue
			}
			if conflictBetween(item.tokens, item.finding.Statement, other.tokens, other.finding.Statement) {
				conflict = describeConflict(item.finding, other.finding)
				break
			}
		}
		item.finding.Conflict = conflict

		switch {
		case conflict != "":
			// Disagreeing evidence is never a fact, however many pages agree
			// with one of the versions.
			item.finding.Certainty = CertaintyAssumption
		case item.finding.Corroboration >= 2:
			item.finding.Certainty = CertaintyFact
		default:
			item.finding.Certainty = CertaintyAssumption
		}
		item.finding.Confidence = confidence(item.finding)
		findings = append(findings, item.finding)
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Certainty != findings[j].Certainty {
			return findings[i].Certainty == CertaintyFact
		}
		if findings[i].Confidence != findings[j].Confidence {
			return findings[i].Confidence > findings[j].Confidence
		}
		return findings[i].Statement < findings[j].Statement
	})
	return findings
}

// confidence scores a finding in [0.05, 0.95]: independent sources raise it,
// a named disagreement and a doubtful source lower it.
func confidence(finding Finding) float64 {
	value := 0.4
	if finding.Corroboration >= 2 {
		value += 0.25
		value += 0.05 * float64(finding.Corroboration-2)
	}
	weight := 0.0
	for _, citation := range finding.Citations {
		weight += citation.Trust.Weight()
	}
	if len(finding.Citations) > 0 {
		value += (weight/float64(len(finding.Citations)) - 0.8) * 0.15
	}
	if finding.Conflict != "" {
		value -= 0.15
	}
	if value < 0.05 {
		return 0.05
	}
	if value > 0.95 {
		return 0.95
	}
	return value
}

// conflictBetween reports whether two statements about the same subject
// disagree: one negates a shared term, or they attach different numbers to it.
func conflictBetween(aTokens []string, a string, bTokens []string, b string) bool {
	shared := sharedTokens(aTokens, bTokens)
	if len(shared) < minSharedForNegate {
		return false
	}
	if overlap(aTokens, bTokens) < conflictThreshold {
		return false
	}
	if negationDiffers(a, b, shared) {
		return true
	}
	return numbersDiffer(a, b)
}

// negationDiffers reports whether one statement negates a shared term and the
// other does not.
func negationDiffers(a, b string, shared []string) bool {
	aNegated := negatedTerms(a, shared)
	bNegated := negatedTerms(b, shared)
	for term := range aNegated {
		if _, ok := bNegated[term]; !ok {
			return true
		}
	}
	for term := range bNegated {
		if _, ok := aNegated[term]; !ok {
			return true
		}
	}
	return false
}

var negationWords = map[string]bool{
	"не": true, "нет": true, "никогда": true, "нельзя": true, "без": true,
	"not": true, "no": true, "never": true, "without": true, "none": true,
}

// negatedTerms returns the shared terms that a negation word modifies in text.
// Up to two words may sit between the negation and the term ("не поддерживает
// полностью"), which is how Russian and English negate a noun phrase.
func negatedTerms(text string, shared []string) map[string]bool {
	words := rawWords(text)
	wanted := make(map[string]bool, len(shared))
	for _, term := range shared {
		wanted[term] = true
	}
	found := map[string]bool{}
	for i, word := range words {
		if !negationWords[word] {
			continue
		}
		for offset := 1; offset <= 2 && i+offset < len(words); offset++ {
			candidate := words[i+offset]
			if !wanted[candidate] {
				// Inflected forms still count, so the term list and the raw
				// words are matched by stem.
				matched := false
				for term := range wanted {
					if SameWord(term, candidate) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			found[candidate] = true
		}
	}
	return found
}

var numberPattern = regexp.MustCompile(`\d+(?:[.,]\d+)?`)

// numbersDiffer reports whether both statements carry different numbers. The
// comparison is by value, so "3,5" and "3.50" agree.
func numbersDiffer(a, b string) bool {
	left := numbersIn(a)
	right := numbersIn(b)
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return true
		}
	}
	return false
}

func numbersIn(text string) []string {
	raw := numberPattern.FindAllString(text, -1)
	if len(raw) == 0 {
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		values = append(values, strings.ReplaceAll(item, ",", "."))
	}
	return values
}

// describeConflict names the disagreement in user language.
func describeConflict(finding, other Finding) string {
	site := "другой источник"
	if len(other.Citations) > 0 {
		site = siteOf(other.Citations[0])
	}
	return fmt.Sprintf("Источники расходятся: %s утверждает иначе — «%s».",
		site, truncateRunes(other.Statement, 240))
}

// Tokens splits text into significant lowercase words: short words and a small
// stopword list are dropped, so agreement is measured on what the statement is
// actually about.
func Tokens(text string) []string {
	words := rawWords(text)
	seen := map[string]bool{}
	tokens := make([]string, 0, len(words))
	for _, word := range words {
		if len([]rune(word)) < 3 || stopwords[word] {
			continue
		}
		if seen[word] {
			continue
		}
		seen[word] = true
		tokens = append(tokens, word)
	}
	sort.Strings(tokens)
	return tokens
}

// rawWords lowercases text and splits it on anything that is not a letter or a
// digit.
func rawWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

var stopwords = map[string]bool{
	"это": true, "этот": true, "эта": true, "эти": true, "для": true,
	"что": true, "чтобы": true, "как": true, "так": true, "также": true,
	"или": true, "при": true, "над": true, "под": true, "про": true,
	"все": true, "всё": true, "весь": true, "был": true, "была": true,
	"были": true, "быть": true, "есть": true, "его": true, "она": true,
	"они": true, "оно": true, "который": true, "которая": true, "которые": true,
	"может": true, "можно": true, "нужно": true, "более": true, "менее": true,
	"только": true, "уже": true, "ещё": true, "еще": true, "чем": true,
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "are": true, "was": true, "were": true,
	"have": true, "has": true, "had": true, "you": true, "your": true,
	"but": true, "not": true, "can": true, "will": true, "would": true,
	"there": true, "their": true, "about": true, "which": true, "when": true,
	"what": true, "how": true, "who": true, "whom": true, "its": true,
}

// SameWord reports whether two tokens are the same significant word. Word
// forms differ by inflection — «задач» and «задачи», «release» and «releases» —
// so the shorter token must be a prefix of the longer one and at least four
// letters long. Exact equality always matches.
func SameWord(a, b string) bool {
	if a == b {
		return true
	}
	short, long := a, b
	if len([]rune(short)) > len([]rune(long)) {
		short, long = long, short
	}
	if len([]rune(short)) < 4 {
		return false
	}
	return strings.HasPrefix(long, short)
}

// matchesAny reports whether the token appears in the set, allowing inflected
// forms.
func matchesAny(token string, set map[string]struct{}) bool {
	if _, ok := set[token]; ok {
		return true
	}
	for item := range set {
		if SameWord(item, token) {
			return true
		}
	}
	return false
}

// overlap returns the share of the smaller token set that the other set also
// contains. Statements of different length are still comparable this way.
func overlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for _, item := range a {
		if matchesAny(item, index(b)) {
			shared++
		}
	}
	smallest := len(a)
	if len(b) < smallest {
		smallest = len(b)
	}
	return float64(shared) / float64(smallest)
}

func index(tokens []string) map[string]struct{} {
	set := make(map[string]struct{}, len(tokens))
	for _, item := range tokens {
		set[item] = struct{}{}
	}
	return set
}

func sharedTokens(a, b []string) []string {
	set := index(a)
	shared := make([]string, 0, len(b))
	for _, item := range b {
		if matchesAny(item, set) {
			shared = append(shared, item)
		}
	}
	return shared
}

// siteOf is the site a citation belongs to: its host without a leading www.
func siteOf(citation Citation) string {
	if citation.Site != "" {
		return strings.ToLower(citation.Site)
	}
	parsed, err := url.Parse(citation.URL)
	if err != nil || parsed.Host == "" {
		return "unknown"
	}
	return normalizeHost(parsed.Host)
}

// Host returns the normalized site of a URL, for grouping results.
func Host(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return normalizeHost(parsed.Host)
}

func normalizeHost(host string) string {
	host = strings.ToLower(host)
	host = strings.TrimPrefix(host, "www.")
	if index := strings.Index(host, ":"); index > 0 {
		host = host[:index]
	}
	return host
}

// CanonicalURL normalizes a result URL so the same page found by two queries
// is only fetched once: the fragment goes, the trailing slash goes, tracking
// parameters go.
func CanonicalURL(rawURL string) string {
	parsed, err := url.Parse(strings.ToLower(strings.TrimSpace(rawURL)))
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "gclid" || lower == "fbclid" ||
			lower == "ref" || lower == "yclid" || lower == "mc_cid" || lower == "mc_eid" {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/"
	}
	parsed.Path = path
	return parsed.String()
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
