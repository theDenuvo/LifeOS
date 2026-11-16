package proactive

// Roadmap stage 5: full proactivity and useful initiative.
//
// Insights answer "what is worth attention"; the outreach engine answers
// "is it worth interrupting the user about it right now". It gates the
// initiative (quiet hours, current busyness, repetition and demotion
// history), starts the dialogue itself in the main chat and records how the
// user reacted, so useless kinds of outreach fade out instead of spamming.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	domainassistant "github.com/theDenuvo/LifeOS/internal/domain/assistant"
	"github.com/theDenuvo/LifeOS/internal/events"
)

// OutreachOutcome is the recorded user reaction to one outreach.
type OutreachOutcome string

const (
	// OutreachPending means the user has not reacted yet.
	OutreachPending OutreachOutcome = "pending"
	// OutreachAcknowledged means the user replied or accepted the point.
	OutreachAcknowledged OutreachOutcome = "acknowledged"
	// OutreachDismissed means the user explicitly said "no".
	OutreachDismissed OutreachOutcome = "dismissed"
	// OutreachSnoozed means the user said "not now".
	OutreachSnoozed OutreachOutcome = "snoozed"
)

// IsValid reports whether the outcome is known.
func (o OutreachOutcome) IsValid() bool {
	switch o {
	case OutreachPending, OutreachAcknowledged, OutreachDismissed, OutreachSnoozed:
		return true
	default:
		return false
	}
}

// Outreach is one self-initiated contact with the user.
type Outreach struct {
	ID             int64
	ConversationID int64
	MessageID      int64
	InsightKey     string
	Kind           string
	Severity       Severity
	Title          string
	Reason         string
	Proposal       string
	CreatedAt      time.Time
	Outcome        OutreachOutcome
	DecidedAt      *time.Time
	SnoozedUntil   *time.Time
}

// OutreachRepository persists self-initiated contacts.
type OutreachRepository interface {
	Create(ctx context.Context, outreach *Outreach) (int64, error)
	Get(ctx context.Context, id int64) (*Outreach, error)
	// Recent returns outreaches created after the given instant, oldest last.
	Recent(ctx context.Context, since time.Time) ([]Outreach, error)
	SetOutcome(ctx context.Context, id int64, outcome OutreachOutcome, decidedAt time.Time) error
	Snooze(ctx context.Context, id int64, until, decidedAt time.Time) error
	List(ctx context.Context, limit, offset int) ([]Outreach, int, error)
}

// SettingsReader is the slice of the settings service that lets the user turn
// the initiative off entirely.
type SettingsReader interface {
	Get(ctx context.Context, key string) (json.RawMessage, error)
}

// SettingsKey is the settings entry that governs proactive outreach.
const SettingsKey = "proactive.outreach"

// OutreachConfig holds the gate thresholds. Zero values fall back to defaults.
type OutreachConfig struct {
	// QuietFrom and QuietTo are local-time hours between which the user is
	// not contacted. QuietFrom >= QuietTo means "from 22 to 8" wrapping
	// midnight; a negative QuietFrom disables quiet hours entirely.
	QuietFrom, QuietTo int
	// MinInterval is the smallest gap between any two outreaches.
	MinInterval time.Duration
	// KindCooldown is how long the same insight kind waits before it may
	// reach out again.
	KindCooldown time.Duration
	// CriticalCooldown is the shorter wait for critical insights.
	CriticalCooldown time.Duration
	// DemoteThreshold is how many dismissals of one kind inside Lookback
	// demote it: the kind stops initiating dialogue and stays panel-only.
	DemoteThreshold int
	// DemoteCooldown is how long a demoted kind stays quiet.
	DemoteCooldown time.Duration
	// Lookback is the window for dismissal statistics.
	Lookback time.Duration
}

// DefaultOutreachConfig is the calm-by-default gate: at most a few useful
// contacts a day, none at night, and kinds the user keeps dismissing stop
// initiating on their own.
func DefaultOutreachConfig() OutreachConfig {
	return OutreachConfig{
		QuietFrom:        22,
		QuietTo:          8,
		MinInterval:      90 * time.Minute,
		KindCooldown:     24 * time.Hour,
		CriticalCooldown: 4 * time.Hour,
		DemoteThreshold:  3,
		DemoteCooldown:   7 * 24 * time.Hour,
		Lookback:         7 * 24 * time.Hour,
	}
}

func (c OutreachConfig) withDefaults() OutreachConfig {
	if c.MinInterval <= 0 {
		c.MinInterval = 90 * time.Minute
	}
	if c.KindCooldown <= 0 {
		c.KindCooldown = 24 * time.Hour
	}
	if c.CriticalCooldown <= 0 {
		c.CriticalCooldown = 4 * time.Hour
	}
	if c.DemoteThreshold <= 0 {
		c.DemoteThreshold = 3
	}
	if c.DemoteCooldown <= 0 {
		c.DemoteCooldown = 7 * 24 * time.Hour
	}
	if c.Lookback <= 0 {
		c.Lookback = 7 * 24 * time.Hour
	}
	return c
}

// OutreachService starts the dialogue on LifeOS's own initiative.
type OutreachService struct {
	uow       UnitOfWork
	assistant assistantUnitOfWork
	calendar  CalendarWatcher
	publisher events.Publisher
	clock     Clock
	timezone  string
	settings  SettingsReader
	cfg       OutreachConfig
	disabled  bool
}

// AssistantTx is the minimal assistant surface the outreach engine uses:
// find the main chat, write one message, read replies and record the outreach
// in the same transaction. It is an interface instead of a direct dependency
// because the assistant application package already imports proactive for the
// context snapshot.
type AssistantTx interface {
	CreateConversation(ctx context.Context, conversation *domainassistant.Conversation) (int64, error)
	GetConversation(ctx context.Context, id int64) (*domainassistant.Conversation, error)
	ListConversations(ctx context.Context, limit, offset int, kind *domainassistant.Kind) ([]*domainassistant.Conversation, int, error)
	TouchConversation(ctx context.Context, id int64, at time.Time) error
	InsertMessage(ctx context.Context, message *domainassistant.Message) (int64, error)
	ListMessages(ctx context.Context, conversationID int64, limit int) ([]*domainassistant.Message, error)
	CreateOutreach(ctx context.Context, outreach *Outreach) (int64, error)
}

// assistantUnitOfWork adapts the assistant storage to the outreach needs.
type assistantUnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx AssistantTx) error) error
}

// OutreachOption customises the outreach service.
type OutreachOption func(*OutreachService)

// WithOutreachClock overrides the clock (tests).
func WithOutreachClock(clock Clock) OutreachOption {
	return func(s *OutreachService) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithOutreachSettings attaches the settings reader for the enable switch.
func WithOutreachSettings(reader SettingsReader) OutreachOption {
	return func(s *OutreachService) { s.settings = reader }
}

// WithOutreachConfig replaces the gate thresholds.
func WithOutreachConfig(cfg OutreachConfig) OutreachOption {
	return func(s *OutreachService) { s.cfg = cfg.withDefaults() }
}

// WithOutreachCalendar attaches the calendar slice used by the busy check.
func WithOutreachCalendar(watcher CalendarWatcher) OutreachOption {
	return func(s *OutreachService) { s.calendar = watcher }
}

// NewOutreachService builds the initiative engine. A nil publisher means
// outreaches are still recorded, just not announced live.
func NewOutreachService(uow UnitOfWork, assistant assistantUnitOfWork, publisher events.Publisher, clock Clock, timezone string, opts ...OutreachOption) *OutreachService {
	if clock == nil {
		clock = systemClock{}
	}
	if timezone == "" {
		timezone = "Europe/Moscow"
	}
	s := &OutreachService{
		uow:       uow,
		assistant: assistant,
		publisher: publisher,
		clock:     clock,
		timezone:  timezone,
		cfg:       DefaultOutreachConfig().withDefaults(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// outreachEnabledError is internal: it aborts the evaluation silently.
type outreachEnabledError struct{}

func (outreachEnabledError) Error() string { return "outreach disabled" }

// Disable turns the engine off permanently for this process: the env switch
// is the hard off, while the runtime setting stays a soft off the user can
// change from the interface.
func (s *OutreachService) Disable() { s.disabled = true }

// Evaluate runs the gate over the current insights and starts the dialogue
// when exactly one contact is justified. It never returns an error: a failed
// initiative only postpones to the next pass, and a gate must not turn into
// an incident for the runner.
func (s *OutreachService) Evaluate(ctx context.Context, insights []Insight) {
	if s.disabled {
		return
	}
	if err := s.evaluate(ctx, insights); err != nil {
		if _, quiet := err.(outreachEnabledError); quiet {
			return
		}
		slog.Warn("outreach: evaluate failed", "error", err)
	}
}

func (s *OutreachService) evaluate(ctx context.Context, insights []Insight) error {
	if s.assistant == nil || s.uow == nil {
		return nil
	}
	if s.settings != nil {
		raw, err := s.settings.Get(ctx, SettingsKey)
		if err == nil && len(raw) > 0 {
			var toggle struct {
				Enabled *bool `json:"enabled"`
			}
			if json.Unmarshal(raw, &toggle) == nil && toggle.Enabled != nil && !*toggle.Enabled {
				return outreachEnabledError{}
			}
		}
	}

	now := s.clock.Now()
	if s.inQuietHours(now) {
		return nil
	}
	if s.busyNow(ctx, now) {
		return nil
	}

	history, err := s.recent(ctx, now)
	if err != nil {
		return err
	}

	candidate := s.pick(insights, history, now)
	if candidate == nil {
		return nil
	}
	return s.start(ctx, *candidate, now)
}

// inQuietHours reports whether the local hour sits inside the night window.
func (s *OutreachService) inQuietHours(now time.Time) bool {
	cfg := s.cfg
	if cfg.QuietFrom < 0 || cfg.QuietTo < 0 || cfg.QuietFrom == cfg.QuietTo {
		return false
	}
	hour := now.In(s.loc()).Hour()
	if cfg.QuietFrom < cfg.QuietTo {
		return hour >= cfg.QuietFrom && hour < cfg.QuietTo
	}
	return hour >= cfg.QuietFrom || hour < cfg.QuietTo
}

// busyNow reports whether a meeting is happening this minute. An initiative
// that interrupts a meeting is noise by definition; the runner will try again.
func (s *OutreachService) busyNow(ctx context.Context, now time.Time) bool {
	if s.calendar == nil {
		return false
	}
	window := 2 * time.Minute
	events, err := s.calendar.ListRange(ctx, now.Add(-window), now.Add(window), nil)
	if err != nil {
		return false
	}
	for _, item := range events {
		if item.Event.StartsAt.Before(now.Add(window)) && item.Event.EndsAt.After(now.Add(-window)) {
			return true
		}
	}
	return false
}

// pick chooses the one insight that justifies contact now, applying
// repetition suppression and demotion history.
func (s *OutreachService) pick(insights []Insight, history []Outreach, now time.Time) *Insight {
	var lastAt time.Time
	kindLast := make(map[string]time.Time)
	keyLast := make(map[string]time.Time)
	// A snooze is a promise: this concrete topic stays silent until then.
	keyBlockedUntil := make(map[string]time.Time)
	dismissals := make(map[string][]time.Time)
	pendingKeys := make(map[string]bool)
	for _, item := range history {
		if item.CreatedAt.After(lastAt) {
			lastAt = item.CreatedAt
		}
		if item.CreatedAt.After(kindLast[item.Kind]) {
			kindLast[item.Kind] = item.CreatedAt
		}
		if item.CreatedAt.After(keyLast[item.InsightKey]) {
			keyLast[item.InsightKey] = item.CreatedAt
		}
		if item.Outcome == OutreachSnoozed && item.SnoozedUntil != nil && item.SnoozedUntil.After(now) {
			if item.SnoozedUntil.After(keyBlockedUntil[item.InsightKey]) {
				keyBlockedUntil[item.InsightKey] = *item.SnoozedUntil
			}
		}
		if item.Outcome == OutreachDismissed && item.DecidedAt != nil {
			dismissals[item.Kind] = append(dismissals[item.Kind], *item.DecidedAt)
		}
		if item.Outcome == OutreachPending {
			pendingKeys[item.InsightKey] = true
		}
	}
	if !lastAt.IsZero() && now.Sub(lastAt) < s.cfg.MinInterval {
		return nil
	}

	demoted := make(map[string]bool)
	for kind, times := range dismissals {
		recent := 0
		for _, at := range times {
			if now.Sub(at) <= s.cfg.Lookback {
				recent++
			}
		}
		if recent >= s.cfg.DemoteThreshold {
			demoted[kind] = true
		}
	}

	var best *Insight
	for i := range insights {
		insight := &insights[i]
		if insight.Severity == SeverityInfo {
			// An opportunity may show in the panel, but it does not start a
			// conversation on its own.
			continue
		}
		if pendingKeys[insight.Key] {
			continue
		}
		// The user said "not now until X": the gate honours X, not just the
		// ordinary cooldown.
		if blocked, ok := keyBlockedUntil[insight.Key]; ok && now.Before(blocked) {
			continue
		}
		if demoted[insight.Kind] {
			continue
		}
		cooldown := s.cfg.KindCooldown
		if insight.Severity == SeverityCritical {
			cooldown = s.cfg.CriticalCooldown
		}
		if last, ok := kindLast[insight.Kind]; ok && now.Sub(last) < cooldown {
			continue
		}
		if last, ok := keyLast[insight.Key]; ok && now.Sub(last) < cooldown {
			continue
		}
		if best == nil || severityRank(insight.Severity) < severityRank(best.Severity) {
			best = insight
		}
	}
	return best
}

// start writes the assistant message and the outreach record in ONE
// transaction, so a crash or a failed write cannot leave an initiative in the
// chat without its history entry — the entry is what prevents a repeat on the
// next pass. The live event goes out only after the commit.
func (s *OutreachService) start(ctx context.Context, insight Insight, now time.Time) error {
	outreach := &Outreach{
		InsightKey: insight.Key,
		Kind:       insight.Kind,
		Severity:   insight.Severity,
		Title:      insight.Title,
		Reason:     insight.Detail,
		Proposal:   insight.Proposal,
		CreatedAt:  now,
		Outcome:    OutreachPending,
	}
	err := s.assistant.Do(ctx, func(ctx context.Context, tx AssistantTx) error {
		kind := domainassistant.KindMain
		conversations, _, err := tx.ListConversations(ctx, 1, 0, &kind)
		if err != nil {
			return err
		}
		if len(conversations) == 0 {
			conversation := domainassistant.NewConversation("Главный чат", now)
			id, err := tx.CreateConversation(ctx, conversation)
			if err != nil {
				return err
			}
			outreach.ConversationID = id
		} else {
			outreach.ConversationID = conversations[0].ID
		}

		content := s.compose(insight)
		message, err := domainassistant.NewMessage(outreach.ConversationID, domainassistant.RoleAssistant, content, now)
		if err != nil {
			return err
		}
		message.Metadata, err = json.Marshal(map[string]any{
			"outreach": map[string]any{
				"insight_key": insight.Key,
				"kind":        insight.Kind,
				"severity":    string(insight.Severity),
			},
		})
		if err != nil {
			return err
		}
		outreach.MessageID, err = tx.InsertMessage(ctx, message)
		if err != nil {
			return err
		}
		if err := tx.TouchConversation(ctx, outreach.ConversationID, now); err != nil {
			return err
		}
		outreach.ID, err = tx.CreateOutreach(ctx, outreach)
		return err
	})
	if err != nil {
		return err
	}

	if s.publisher != nil {
		if err := s.publisher.Publish("outreach.created", map[string]any{
			"id":              outreach.ID,
			"conversation_id": outreach.ConversationID,
			"message_id":      outreach.MessageID,
			"insight_key":     outreach.InsightKey,
			"kind":            outreach.Kind,
			"severity":        string(outreach.Severity),
			"title":           outreach.Title,
		}); err != nil {
			// The outreach is durable; a lost live event only delays the toast.
			slog.Error("publish outreach failed", "id", outreach.ID, "error", err)
		}
	}
	slog.Info("outreach started", "id", outreach.ID, "key", outreach.InsightKey)
	return nil
}

// compose renders the brief: why LifeOS reached out and what it proposes.
func (s *OutreachService) compose(insight Insight) string {
	var b strings.Builder
	b.WriteString(insight.Title)
	b.WriteString(". ")
	if insight.Detail != "" {
		b.WriteString(insight.Detail)
		if !strings.HasSuffix(insight.Detail, ".") {
			b.WriteString(".")
		}
		b.WriteString(" ")
	}
	if insight.Proposal != "" {
		b.WriteString("Предлагаю: ")
		b.WriteString(insight.Proposal)
		b.WriteString(". ")
	}
	b.WriteString("Если сейчас не время — скажите «не сейчас», и я не буду возвращаться к этому сегодня.")
	return b.String()
}

func (s *OutreachService) loc() *time.Location {
	loc, err := time.LoadLocation(s.timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (s *OutreachService) recent(ctx context.Context, now time.Time) ([]Outreach, error) {
	var history []Outreach
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Outreach().Recent(ctx, now.Add(-s.cfg.Lookback))
		if err != nil {
			return err
		}
		history = found
		return nil
	})
	return history, err
}

// List returns the outreach history for the workspace.
func (s *OutreachService) List(ctx context.Context, limit, offset int) ([]Outreach, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var (
		outreach []Outreach
		total    int
	)
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, count, err := tx.Outreach().List(ctx, limit, offset)
		if err != nil {
			return err
		}
		outreach, total = found, count
		return nil
	})
	return outreach, total, err
}

// Get returns one outreach.
func (s *OutreachService) Get(ctx context.Context, id int64) (*Outreach, error) {
	var found *Outreach
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		item, err := tx.Outreach().Get(ctx, id)
		if err != nil {
			return err
		}
		found = item
		return nil
	})
	return found, err
}

// ErrOutreachNotFound is returned when the outreach does not exist.
var ErrOutreachNotFound = errors.New("outreach not found")

// Acknowledge records that the user accepted the point.
func (s *OutreachService) Acknowledge(ctx context.Context, id int64) error {
	return s.decide(ctx, id, OutreachAcknowledged)
}

// Dismiss records an explicit "no". Enough dismissals of one kind teach the
// gate to stop initiating it.
func (s *OutreachService) Dismiss(ctx context.Context, id int64) error {
	return s.decide(ctx, id, OutreachDismissed)
}

// Snooze records a "not now" until the given instant. Only a pending
// outreach can be snoozed: a decided reaction is final.
func (s *OutreachService) Snooze(ctx context.Context, id int64, until time.Time) error {
	now := s.clock.Now()
	if until.Before(now) {
		until = now.Add(24 * time.Hour)
	}
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Outreach().Get(ctx, id)
		if err != nil {
			return err
		}
		if found == nil {
			return ErrOutreachNotFound
		}
		if found.Outcome != OutreachPending {
			return fmt.Errorf("outreach %d already %s", id, found.Outcome)
		}
		return tx.Outreach().Snooze(ctx, id, until, now)
	})
}

func (s *OutreachService) decide(ctx context.Context, id int64, outcome OutreachOutcome) error {
	now := s.clock.Now()
	return s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Outreach().Get(ctx, id)
		if err != nil {
			return err
		}
		if found == nil {
			return ErrOutreachNotFound
		}
		if found.Outcome != OutreachPending {
			return fmt.Errorf("outreach %d already %s", id, found.Outcome)
		}
		return tx.Outreach().SetOutcome(ctx, id, outcome, now)
	})
}

// AcknowledgeReplies reads the user's first reply to each pending outreach
// and records what it actually said. The message itself invites a "не сейчас",
// so a reply must not silently become agreement: explicit refusals become
// dismissals, deferrals become snoozes, everything else counts as accepted.
func (s *OutreachService) AcknowledgeReplies(ctx context.Context) {
	if err := s.acknowledgeReplies(ctx); err != nil {
		slog.Warn("outreach: acknowledge replies failed", "error", err)
	}
}

func (s *OutreachService) acknowledgeReplies(ctx context.Context) error {
	now := s.clock.Now()
	var pending []Outreach
	err := s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
		found, err := tx.Outreach().Recent(ctx, now.Add(-s.cfg.Lookback))
		if err != nil {
			return err
		}
		pending = found
		return nil
	})
	if err != nil {
		return err
	}
	for _, item := range pending {
		if item.Outcome != OutreachPending {
			continue
		}
		reply, err := s.firstReply(ctx, item)
		if err != nil {
			continue
		}
		if reply == nil {
			continue
		}
		outcome := classifyReply(reply.Content)
		err = s.uow.Do(ctx, func(ctx context.Context, tx Transaction) error {
			if outcome == OutreachSnoozed {
				return tx.Outreach().Snooze(ctx, item.ID, now.Add(24*time.Hour), now)
			}
			return tx.Outreach().SetOutcome(ctx, item.ID, outcome, now)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// replyScanDepth is how much of the recent conversation the reply check
// reads. The scan takes the tail, so an early reply survives long later
// conversations.
const replyScanDepth = 500

// firstReply returns the first user message posted after the outreach's own
// message, or nil when the user has not answered yet.
func (s *OutreachService) firstReply(ctx context.Context, item Outreach) (*domainassistant.Message, error) {
	var reply *domainassistant.Message
	err := s.assistant.Do(ctx, func(ctx context.Context, tx AssistantTx) error {
		messages, err := tx.ListMessages(ctx, item.ConversationID, replyScanDepth)
		if err != nil {
			return err
		}
		for _, message := range messages {
			if message.Role != domainassistant.RoleUser {
				continue
			}
			if message.ID > item.MessageID {
				reply = message
				break
			}
		}
		return nil
	})
	return reply, err
}

// classifyReply maps the user's wording onto an outcome. The message invites
// "не сейчас", so deferrals and refusals must be distinguished from consent.
func classifyReply(text string) OutreachOutcome {
	normalized := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	hardRefusal := []string{
		"не надо", "не нужно", "не стоит", "не беспоко", "не обращай",
		"хватит", "отмен", "забудь", "не хочу", "no", "don't", "stop",
	}
	for _, marker := range hardRefusal {
		if strings.Contains(normalized, marker) {
			return OutreachDismissed
		}
	}
	deferral := []string{
		"не сейчас", "позже", "потом", "не сегодня", "завтра", "вечером",
		"после", "сначала", "later", "not now",
	}
	for _, marker := range deferral {
		if strings.Contains(normalized, marker) {
			return OutreachSnoozed
		}
	}
	return OutreachAcknowledged
}
