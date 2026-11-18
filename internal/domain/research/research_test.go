package research

import (
	"encoding/json"
	"errors"
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
		Read:      true,
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
		{Statement: "Canberra is the capital city of Australia.", Citation: citation("https://a.example.org/canberra", "Canberra")},
		{Statement: "The city occupies the northern part of the Australian Capital Territory.", Citation: citation("https://a.example.org/canberra", "Canberra")},
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

// Two sources that name different founders are not confirming each other: the
// shared words are the subject and the verb, and the values are the whole
// point. Merging them would report a fact that no source states.
func TestBuildFindingsSubstitutedValueIsNotAFact(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
	}{
		{
			name: "person",
			a:    "OpenAI was founded by Sam Altman.",
			b:    "OpenAI was founded by Elon Musk.",
		},
		{
			name: "organisation",
			a:    "The project is maintained by the Cloudflare Workers team.",
			b:    "The project is maintained by the Deno Land team.",
		},
		{
			name: "year",
			a:    "The language was released in 2015 with a small standard library.",
			b:    "The language was released in 2016 with a small standard library.",
		},
		{
			name: "amount",
			a:    "Бесплатный тариф включает 100 запросов в минуту для одного ключа.",
			b:    "Бесплатный тариф включает 300 запросов в минуту для одного ключа.",
		},
		{
			name: "negation",
			a:    "The free plan includes access to the analytics dashboard.",
			b:    "The free plan does not include access to the analytics dashboard.",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			findings := BuildFindings([]Candidate{
				{Statement: testCase.a, Citation: citation("https://a.example.org/x", "A")},
				{Statement: testCase.b, Citation: citation("https://b.example.net/x", "B")},
			})
			if len(findings) != 2 {
				t.Fatalf("statements with different values must stay apart, got %d: %+v", len(findings), findings)
			}
			for _, finding := range findings {
				if finding.Certainty == CertaintyFact {
					t.Fatalf("contradicting statements are never a fact: %+v", finding)
				}
				if finding.Conflict == "" {
					t.Fatalf("the disagreement must be named for %q", finding.Statement)
				}
				if len(finding.Citations) != 1 {
					t.Fatalf("each statement keeps its own source, got %+v", finding.Citations)
				}
			}
		})
	}
}

// A statement that names more than the other is still the same claim: "the
// release" and "the release in Go 1.24" do not disagree.
// Names are not always capitalised: forum posts, transcripts and generated
// pages write "sam altman" in lower case, where a capitalisation heuristic
// sees no value at all. Two statements that fill the same slot with different
// words are a disagreement in any case.
func TestBuildFindingsLowercaseValuesStayApart(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
	}{
		{
			name: "lowercase person",
			a:    "the openai company was founded by sam altman and launched its first product in 2020.",
			b:    "the openai company was founded by elon musk and launched its first product in 2020.",
		},
		{
			name: "lowercase organisation",
			a:    "метрики сервиса доступны в grafana cloud.",
			b:    "метрики сервиса доступны в datadog.",
		},
		{
			name: "lowercase product",
			a:    "the build runs on the runner image by default.",
			b:    "the build runs on the container image by default.",
		},
		{
			name: "same context, different value",
			a:    "the endpoint answers in 200 milliseconds under load.",
			b:    "the endpoint answers in 400 milliseconds under load.",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			findings := BuildFindings([]Candidate{
				{Statement: testCase.a, Citation: citation("https://a.example.org/x", "A")},
				{Statement: testCase.b, Citation: citation("https://b.example.net/x", "B")},
			})
			if len(findings) != 2 {
				t.Fatalf("different values must stay apart, got %d: %+v", len(findings), findings)
			}
			for _, finding := range findings {
				if finding.Certainty == CertaintyFact {
					t.Fatalf("contradicting statements are never a fact: %+v", finding)
				}
				if finding.Conflict == "" {
					t.Fatalf("the disagreement must be named for %q", finding.Statement)
				}
			}
		})
	}
}

// The same name written in different cases is one name, not a substitution:
// one site writes "Sam Altman", the other "sam altman", and they confirm each
// other. Word forms of one name are the same name too.
func TestBuildFindingsSameValueInAnotherCaseOrFormAgrees(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
	}{
		{
			name: "case",
			a:    "the openai company was founded by Sam Altman.",
			b:    "the openai company was founded by sam altman.",
		},
		{
			name: "word form",
			a:    "Компанию основал сэм альтман в 2015 году.",
			b:    "Компанию основал сэма альтмана в 2015 году.",
		},
		{
			name: "organisation case",
			a:    "Метрики сервиса доступны в Grafana Cloud.",
			b:    "метрики сервиса доступны в grafana cloud.",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			findings := BuildFindings([]Candidate{
				{Statement: testCase.a, Citation: citation("https://a.example.org/x", "A")},
				{Statement: testCase.b, Citation: citation("https://b.example.net/x", "B")},
			})
			if len(findings) != 1 {
				t.Fatalf("the same value is one claim, got %d: %+v", len(findings), findings)
			}
			if findings[0].Certainty != CertaintyFact {
				t.Fatalf("two independent sites are a fact, got %s", findings[0].Certainty)
			}
		})
	}
}

func TestBuildFindingsExtraDetailStillAgrees(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "The release adds generic type aliases to the language.", Citation: citation("https://a.example.org/1", "A")},
		{Statement: "In Go 1.24 the release adds generic type aliases to the language.", Citation: citation("https://b.example.net/1", "B")},
	})
	if len(findings) != 1 {
		t.Fatalf("one claim with extra detail must merge, got %d: %+v", len(findings), findings)
	}
	if findings[0].Certainty != CertaintyFact {
		t.Fatalf("two independent sites are a fact, got %s", findings[0].Certainty)
	}
}

// A search snippet is a lead. Two unread snippets that agree are still two
// leads, so the finding stays an assumption until a page is actually read.
func TestBuildFindingsTwoSnippetsAreNotAFact(t *testing.T) {
	snippet := func(url string) Citation {
		item := citation(url, "snippet")
		item.Read = false
		item.Trust = TrustLow
		return item
	}
	findings := BuildFindings([]Candidate{
		{Statement: "Canberra is the capital city of Australia.", Citation: snippet("https://a.example.org/canberra")},
		{Statement: "Canberra is the capital city of Australia.", Citation: snippet("https://b.example.net/canberra")},
	})
	if len(findings) != 1 {
		t.Fatalf("the statements agree, got %d findings", len(findings))
	}
	if findings[0].Certainty == CertaintyFact {
		t.Fatalf("two unread snippets are not a fact: %+v", findings[0])
	}
	if findings[0].Corroboration != 2 {
		t.Fatalf("both sites still support the claim, got %d", findings[0].Corroboration)
	}
	// One read page is enough to make the claim a fact.
	findings = BuildFindings([]Candidate{
		{Statement: "Canberra is the capital city of Australia.", Citation: citation("https://a.example.org/canberra", "A")},
		{Statement: "Canberra is the capital city of Australia.", Citation: snippet("https://b.example.net/canberra")},
	})
	if findings[0].Certainty != CertaintyFact {
		t.Fatalf("a read page plus an independent snippet is a fact, got %s", findings[0].Certainty)
	}
}

// Two subdomains of one publisher are one site: a blog and a documentation
// site do not corroborate each other.
func TestBuildFindingsSubdomainsAreOneSite(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "The command supports a dry run flag that prints the plan first.", Citation: citation("https://blog.example.org/dry-run", "blog")},
		{Statement: "A dry run flag prints the plan first, the command supports it.", Citation: citation("https://docs.example.org/dry-run", "docs")},
	})
	if len(findings) != 1 {
		t.Fatalf("the statements agree, got %d findings", len(findings))
	}
	if findings[0].Corroboration != 1 {
		t.Fatalf("one publisher is one site, got %d", findings[0].Corroboration)
	}
	if findings[0].Certainty == CertaintyFact {
		t.Fatalf("a publisher cannot confirm itself: %+v", findings[0])
	}
	if findings[0].Domains[0] != "example.org" {
		t.Fatalf("domains are reported per registrable site, got %v", findings[0].Domains)
	}
}

func TestEntitiesFindValuesNotCapitalisation(t *testing.T) {
	entities := Entities("The report from OpenAI says Go 1.24 shipped 240 kilobytes.")
	joined := strings.Join(entities, ",")
	for _, wanted := range []string{"openai", "go", "1.24", "240"} {
		if !strings.Contains(joined, wanted) {
			t.Fatalf("entity %q is missing from %v", wanted, entities)
		}
	}
	if strings.Contains(joined, "the") || strings.Contains(joined, "report") {
		t.Fatalf("sentence openers and common nouns are not values: %v", entities)
	}
	if got := Entities("В отчёте сказано, что Kestrel 2.0 вышел в 2024 году."); !containsEntity(got, "2") {
		t.Fatalf("a version is a value: %v", got)
	}
}

func TestBuildFindingsNegationIsAConflict(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "Планировщик поддерживает повторяющиеся задачи с гибким интервалом.", Citation: citation("https://a.example.org/1", "A")},
		{Statement: "Планировщик не поддерживает повторяющиеся задачи с гибким интервалом.", Citation: citation("https://b.example.net/1", "B")},
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
		{Statement: "Бесплатный тариф включает 100 запросов в минуту для одного ключа.", Citation: citation("https://a.example.org/limits", "limits")},
		{Statement: "Бесплатный тариф включает 300 запросов в минуту для одного ключа.", Citation: citation("https://b.example.net/limits", "limits")},
	})
	if len(findings) != 2 {
		t.Fatalf("expected two findings, got %d", len(findings))
	}
	if findings[0].Conflict == "" || findings[1].Conflict == "" {
		t.Fatalf("expected both findings to report the disagreement: %+v", findings)
	}
}

func TestBuildFindingsIgnoresEmptyStatements(t *testing.T) {
	findings := BuildFindings([]Candidate{{Statement: "   ", Citation: citation("https://a.example.org", "A")}})
	if len(findings) != 0 {
		t.Fatalf("empty statements must be dropped, got %+v", findings)
	}
}

func TestBuildFindingsOrdersFactsFirst(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "Perf matters for large deployments running on small machines.", Citation: citation("https://a.example.org/1", "A")},
		{Statement: "The command supports a dry run flag that prints the plan first.", Citation: citation("https://b.example.net/1", "B")},
		{Statement: "A dry run flag prints the plan first, the command supports it.", Citation: citation("https://c.example.io/1", "C")},
	})
	if findings[0].Certainty != CertaintyFact {
		t.Fatalf("facts come first, got %s: %+v", findings[0].Certainty, findings)
	}
}

// A citation saved before the reading-status field existed must not claim the
// page was unread: LifeOS does not know, and "неизвестно" is not "нет".
// TestBuildFindingsSimilarNamesStayApart pins the difference between names and
// word forms: "John" and "Johnson" share a prefix the way inflected forms do,
// but they are two people, and a fact built from both would name someone who
// exists in neither source.
func TestBuildFindingsSimilarNamesStayApart(t *testing.T) {
	cases := []struct{ first, second string }{
		// lowercase: no capitalisation hint exists at all
		{"The company was founded by john smith in 2020.",
			"The company was founded by johnson smith in 2020."},
		// capitalised: the entity comparison must not use the prefix either
		{"The company was founded by John Smith in 2020.",
			"The company was founded by Johnson Smith in 2020."},
		{"Anna holds the majority stake in the company since 2019.",
			"Annabelle holds the majority stake in the company since 2019."},
		// a long statement where the shared words alone would clear the merge
		// threshold and the substituted slot is two lowercase words
		{"In a recent interview the founder sam altman said the company was founded in 2020 and keeps growing fast every year since then.",
			"In a recent interview the founder elon musk said the company was founded in 2020 and keeps growing fast every year since then."},
	}
	for _, c := range cases {
		findings := BuildFindings([]Candidate{
			{Statement: c.first, Citation: Citation{URL: "https://alpha.example.com/a", Site: "alpha.example.com", Read: true}},
			{Statement: c.second, Citation: Citation{URL: "https://beta.example.com/b", Site: "beta.example.com", Read: true}},
		})
		if len(findings) != 2 {
			t.Fatalf("%q / %q merged into %d findings", c.first, c.second, len(findings))
		}
		for _, finding := range findings {
			if finding.Certainty == CertaintyFact {
				t.Fatalf("%q / %q became a fact: %q", c.first, c.second, finding.Statement)
			}
			if finding.Conflict == "" {
				t.Fatalf("%q / %q disagree without naming the disagreement", c.first, c.second)
			}
		}
	}
}

// TestBuildFindingsWordFormsStillAgree is the other side of the same rule:
// inflected forms of one name are one name, and stricter comparison must not
// split them into a disagreement.
func TestBuildFindingsWordFormsStillAgree(t *testing.T) {
	findings := BuildFindings([]Candidate{
		{Statement: "Сэм Альтман основал компанию в 2015 году.", Citation: Citation{URL: "https://alpha.example.org/a", Site: "alpha.example.org", Read: true}},
		{Statement: "Компанию основал Сэма Альтмана в 2015.", Citation: Citation{URL: "https://beta.example.net/b", Site: "beta.example.net", Read: true}},
	})
	if len(findings) != 1 {
		t.Fatalf("word forms of one name split into %d findings: %+v", len(findings), findings)
	}
	if findings[0].Certainty != CertaintyFact {
		t.Fatalf("corroborated word forms lost the fact: %q", findings[0].Certainty)
	}
}

func TestLegacyCitationKeepsReadingStatusUnknown(t *testing.T) {
	legacy := `{"url":"https://go.dev/doc/go1.24","title":"Go 1.24","site":"go.dev",` +
		`"excerpt":"…","fetched_at":"2025-03-01T09:00:00Z","trust":"normal"}`
	var citation Citation
	if err := json.Unmarshal([]byte(legacy), &citation); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !citation.ReadUnknown {
		t.Fatalf("a citation without the field has an unknown status: %+v", citation)
	}
	if citation.Read {
		t.Fatalf("unknown is not read: %+v", citation)
	}
	encoded, err := json.Marshal(citation)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(encoded), `"read_unknown":true`) {
		t.Fatalf("the round trip must keep the unknown status: %s", encoded)
	}
	if strings.Contains(string(encoded), `"read":`) {
		t.Fatalf("an unknown status must not claim read or unread: %s", encoded)
	}

	// A new citation states its status plainly, and re-reading keeps it.
	fresh := `{"url":"https://go.dev/doc/go1.24","fetched_at":"2025-03-01T09:00:00Z","trust":"normal","read":false}`
	citation = Citation{}
	if err := json.Unmarshal([]byte(fresh), &citation); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if citation.ReadUnknown || citation.Read {
		t.Fatalf("a stated status is known: %+v", citation)
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
		// Only the scheme and the host are case-insensitive: paths and query
		// values must survive byte for byte, or the link stops working.
		"https://Example.com/Users/Nikita?token=AbCd123": "https://example.com/Users/Nikita?token=AbCd123",
		"HTTPS://Example.COM/Docs/Go1.24":                "https://example.com/Docs/Go1.24",
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
	watch := &Watch{Seen: []Fingerprint{{URL: "https://a.example.org", Hash: "same", FirstSeen: now.Add(-time.Hour)}}}
	diff, _ := watch.Diff([]Fingerprint{{URL: "https://a.example.org", Hash: "same", FirstSeen: now}})
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

// Which tools of an MCP server may be used without a write is the user's
// decision, not the server's claim about itself.
func TestConnectionToolAllowlist(t *testing.T) {
	connection := Connection{
		Name: "MCP", Kind: KindMCP, Mode: ModeRead,
		Endpoint: "https://mcp.example.com/mcp", AllowedTools: []string{"read_repo", "list_issues"},
	}
	if err := connection.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !connection.AllowsTool("read_repo") || connection.AllowsTool("delete_repo") {
		t.Fatalf("the allowlist decides: %+v", connection.AllowedTools)
	}
	bad := connection
	bad.AllowedTools = []string{"read_repo", "  "}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidTool) {
		t.Fatalf("a blank tool name is invalid, got %v", err)
	}
	bad.AllowedTools = []string{strings.Repeat("x", 201)}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidTool) {
		t.Fatalf("an oversized tool name is invalid, got %v", err)
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
