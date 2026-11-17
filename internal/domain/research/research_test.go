package research

import (
	"strings"
	"testing"
	"time"
)

func citation(url, title string) Citation {
	return Citation{
		URL:       url,
		Title:     title,
		Site:      Host(url),
		Excerpt:   title,
		FetchedAt: time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC),
		Trust:     TrustNormal,
	}
}

func TestBuildFindingsCorroboratedStatementIsAFact(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "Go 1.24 adds support for generic type aliases in the language.", Citation: citation("https://go.dev/doc/go1.24", "Go 1.24")},
		{Statement: "Generic type aliases arrive in Go 1.24, the language adds support for them.", Citation: citation("https://example.org/go-124", "Go 1.24 review")},
		{Statement: "The release also ships a faster garbage collector for small heaps.", Citation: citation("https://example.net/gc", "GC notes")},
	})

	if len(findings) != 2 {
		t.Fatalf("expected the agreeing statements to merge, got %d findings: %+v", len(findings), findings)
	}
	corroborated := findings[0]
	if corroborated.Certainty != CertaintyFact {
		t.Fatalf("two independent sites must produce a fact, got %s", corroborated.Certainty)
	}
	if corroborated.Corroboration != 2 {
		t.Fatalf("expected corroboration 2, got %d", corroborated.Corroboration)
	}
	if len(corroborated.Citations) != 2 {
		t.Fatalf("both citations must be kept, got %d", len(corroborated.Citations))
	}
	if findings[1].Certainty != CertaintyAssumption {
		t.Fatalf("a single source is an assumption, got %s", findings[1].Certainty)
	}
	if findings[1].Conflict != "" {
		t.Fatalf("unrelated statements must not conflict: %q", findings[1].Conflict)
	}
}

func TestBuildFindingsSameSiteIsNotIndependent(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "The library supports streaming responses over HTTP/2.", Citation: citation("https://docs.example.com/a", "A")},
		{Statement: "Streaming responses over HTTP/2 are supported by the library.", Citation: citation("https://docs.example.com/b", "B")},
	})
	if len(findings) != 1 {
		t.Fatalf("same-site statements merge into one finding, got %d", len(findings))
	}
	if findings[0].Certainty != CertaintyAssumption {
		t.Fatalf("two pages on one site are not independent, got %s", findings[0].Certainty)
	}
	if findings[0].Corroboration != 1 {
		t.Fatalf("one site is one source, got %d", findings[0].Corroboration)
	}
}

// Two claims of one page are not one claim: the page is quoted sentence by
// sentence, so the report must not lose a sentence by merging it into another.
func TestBuildFindingsKeepsDifferentClaimsOfOnePageApart(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "Canberra is the capital city of Australia.", Citation: citation("https://a.example.com/canberra", "Canberra")},
		{Statement: "The city occupies the northern part of the Australian Capital Territory.", Citation: citation("https://a.example.com/canberra", "Canberra")},
	})
	if len(findings) != 2 {
		t.Fatalf("different claims stay separate, got %d: %+v", len(findings), findings)
	}
	for _, finding := range findings {
		if finding.Corroboration != 1 {
			t.Fatalf("one page is one source, got %d", finding.Corroboration)
		}
		if finding.Certainty == CertaintyFact {
			t.Fatalf("one page cannot confirm itself: %+v", finding)
		}
	}
}

func TestBuildFindingsNegationIsAConflict(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "Планировщик поддерживает повторяющиеся задачи с гибким интервалом.", Citation: citation("https://a.example.com/1", "A")},
		{Statement: "Планировщик не поддерживает повторяющиеся задачи с гибким интервалом.", Citation: citation("https://b.example.com/1", "B")},
	})
	if len(findings) != 2 {
		t.Fatalf("disagreeing statements stay separate, got %d", len(findings))
	}
	for _, finding := range findings {
		if finding.Conflict == "" {
			t.Fatalf("expected a named disagreement for %q", finding.Statement)
		}
		if finding.Certainty != CertaintyAssumption {
			t.Fatalf("disputed evidence is never a fact, got %s", finding.Certainty)
		}
	}
}

func TestBuildFindingsDifferentNumbersConflict(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "Бесплатный тариф включает 100 запросов в минуту для одного ключа.", Citation: citation("https://a.example.com/limits", "limits")},
		{Statement: "Бесплатный тариф включает 300 запросов в минуту для одного ключа.", Citation: citation("https://b.example.com/limits", "limits")},
	})
	if len(findings) != 2 {
		t.Fatalf("expected two findings, got %d", len(findings))
	}
	if findings[0].Conflict == "" || findings[1].Conflict == "" {
		t.Fatalf("expected both findings to report the disagreement: %+v", findings)
	}
}

func TestBuildFindingsIgnoresEmptyStatements(t *testing.T) {
	findings := BuildFindings([]Candidate{{Statement: "   ", Citation: citation("https://a.example.com", "A")}})
	if len(findings) != 0 {
		t.Fatalf("empty statements must be dropped, got %+v", findings)
	}
}

func TestBuildFindingsOrdersFactsFirst(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "Perf matters for large deployments running on small machines.", Citation: citation("https://a.example.com/1", "A")},
		{Statement: "The command supports a dry run flag that prints the plan first.", Citation: citation("https://b.example.com/1", "B")},
		{Statement: "A dry run flag prints the plan first, the command supports it.", Citation: citation("https://c.example.com/1", "C")},
	})
	if findings[0].Certainty != CertaintyFact {
		t.Fatalf("facts come first, got %s: %+v", findings[0].Certainty, findings)
	}
}

func TestTokensDropsStopwordsAndDuplicates(t *testing.T) {
	tokens := Tokens("Этот планировщик для задач и задач — the scheduler for tasks")
	joined := strings.Join(tokens, ",")
	if strings.Contains(joined, "этот") || strings.Contains(joined, "для") || strings.Contains(joined, "the") {
		t.Fatalf("stopwords must be dropped: %q", joined)
	}
	count := 0
	for _, token := range tokens {
		if token == "задач" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicates must collapse, got %q", joined)
	}
}

func TestCanonicalURLStripsTrackingAndFragment(t *testing.T) {
	cases := map[string]string{
		"https://Example.com/doc/?utm_source=news&b=2#intro": "https://example.com/doc?b=2",
		"https://example.com/doc":                            "https://example.com/doc",
		"https://example.com/":                               "https://example.com/",
	}
	for input, want := range cases {
		if got := CanonicalURL(input); got != want {
			t.Fatalf("CanonicalURL(%q) = %q, want %q", input, got, want)
		}
	}
	if CanonicalURL("not a url") != "" {
		t.Fatal("invalid URLs canonicalize to an empty string")
	}
}

func TestWatchDiffDetectsNewAndUpdatedPages(t *testing.T) {
	now := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	watch := &Watch{
		Topic: "Go releases",
		Seen: []Fingerprint{
			{URL: "https://go.dev/doc", Hash: "old", FirstSeen: now.Add(-48 * time.Hour)},
			{URL: "https://example.org/gone", Hash: "gone", FirstSeen: now.Add(-72 * time.Hour)},
		},
	}
	diff, merged := watch.Diff([]Fingerprint{
		{URL: "https://go.dev/doc", Hash: "new", FirstSeen: now},
		{URL: "https://example.com/fresh", Hash: "x", FirstSeen: now},
	})
	if len(diff.Updated) != 1 || diff.Updated[0].URL != "https://go.dev/doc" {
		t.Fatalf("expected one updated page, got %+v", diff)
	}
	if len(diff.New) != 1 || diff.New[0].URL != "https://example.com/fresh" {
		t.Fatalf("expected one new page, got %+v", diff)
	}
	if !diff.Changed() {
		t.Fatal("a diff with new and updated pages changed")
	}
	if !strings.Contains(diff.Summary(), "новых источников: 1") || !strings.Contains(diff.Summary(), "обновлено: 1") {
		t.Fatalf("unexpected summary %q", diff.Summary())
	}
	if len(merged) != 3 {
		t.Fatalf("the baseline keeps the vanished page, got %d entries", len(merged))
	}
	if merged[0].FirstSeen.Before(merged[2].FirstSeen) {
		t.Fatal("the baseline is ordered newest first")
	}
}

func TestWatchDiffWithoutChanges(t *testing.T) {
	now := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	watch := &Watch{Seen: []Fingerprint{{URL: "https://a.example.com", Hash: "same", FirstSeen: now.Add(-time.Hour)}}}
	diff, _ := watch.Diff([]Fingerprint{{URL: "https://a.example.com", Hash: "same", FirstSeen: now}})
	if diff.Changed() {
		t.Fatalf("identical content is not a change: %+v", diff)
	}
	if diff.Unchanged != 1 {
		t.Fatalf("expected one unchanged page, got %d", diff.Unchanged)
	}
	if diff.Summary() != "Изменений нет" {
		t.Fatalf("unexpected summary %q", diff.Summary())
	}
}

func TestWatchDiffBoundsTheBaseline(t *testing.T) {
	now := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	watch := &Watch{}
	fresh := make([]Fingerprint, 0, maxSeen+20)
	for i := 0; i < maxSeen+20; i++ {
		fresh = append(fresh, Fingerprint{
			URL:       "https://example.com/" + strings.Repeat("a", 3) + string(rune('a'+i%26)) + "-" + itoa(i),
			Hash:      "h",
			FirstSeen: now.Add(time.Duration(i) * time.Second),
		})
	}
	_, merged := watch.Diff(fresh)
	if len(merged) != maxSeen {
		t.Fatalf("baseline is bounded to %d, got %d", maxSeen, len(merged))
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func TestConnectionValidation(t *testing.T) {
	valid := &Connection{Name: "Docs", Kind: KindHTTPAPI, Mode: ModeRead, Endpoint: "https://api.example.com/v1"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid connection rejected: %v", err)
	}
	if valid.Trust != TrustNormal {
		t.Fatal("trust defaults to normal")
	}

	bad := []*Connection{
		{Name: "", Kind: KindHTTPAPI, Mode: ModeRead, Endpoint: "https://api.example.com"},
		{Name: "X", Kind: "carrier-pigeon", Mode: ModeRead, Endpoint: "https://api.example.com"},
		{Name: "X", Kind: KindHTTPAPI, Mode: "admin", Endpoint: "https://api.example.com"},
		{Name: "X", Kind: KindHTTPAPI, Mode: ModeRead, Endpoint: ""},
		{Name: "X", Kind: KindHTTPAPI, Mode: ModeRead, Endpoint: "ftp://api.example.com"},
		{Name: "X", Kind: KindWebSearch, Mode: ModeRead, Endpoint: "https://search.example.com/search?q={query}"},
		{Name: "X", Kind: KindScript, Mode: ModeWrite, Endpoint: "curl example.com"},
		{Name: "X", Kind: KindScript, Mode: ModeRead, Endpoint: "echo a\necho b"},
	}
	for _, item := range bad {
		if err := item.Validate(); err == nil {
			t.Fatalf("expected %+v to be rejected", item)
		}
	}

	search := &Connection{Name: "Searx", Kind: KindWebSearch, Mode: ModeRead, Format: FormatSearxng, Endpoint: "https://search.example.com/search?q={query}&format=json"}
	if err := search.Validate(); err != nil {
		t.Fatalf("valid search source rejected: %v", err)
	}
}

func TestNextCheckFollowsCadence(t *testing.T) {
	now := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	daily := &Watch{Enabled: true, Cadence: CadenceDaily}
	next := daily.NextCheck(now)
	if next == nil || next.Sub(now) != 24*time.Hour {
		t.Fatalf("daily watch checks in 24h, got %v", next)
	}
	manual := &Watch{Enabled: true, Cadence: CadenceManual}
	if manual.NextCheck(now) != nil {
		t.Fatal("a manual watch has no next check")
	}
	disabled := &Watch{Enabled: false, Cadence: CadenceDaily}
	if disabled.NextCheck(now) != nil {
		t.Fatal("a disabled watch has no next check")
	}
}
