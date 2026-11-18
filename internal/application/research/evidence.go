package research

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

// This file holds the deterministic reading of the evidence: which passage of
// a page answers the question, how a page is fingerprinted for a watch, and
// how the collected findings are written down. None of it calls a model, so
// the same pages always produce the same report.

const (
	// passageLimit keeps one statement readable: a claim longer than this has
	// stopped being a quote and become a summary.
	passageLimit = 420
	// minPassageRunes drops fragments too short to state anything.
	minPassageRunes = 60
	// minPassageScore is the share of query words a passage must contain to be
	// treated as an answer to the query.
	minPassageScore = 0.34
	// maxQueries bounds the search fan-out of one run.
	maxQueries = 3
	// maxClaimsPerPage bounds how many sentences of one page become claims:
	// the report stays readable, and a page cannot drown another one out.
	maxClaimsPerPage = 3
	// minClaimRunes drops a sentence too short to state anything on its own.
	minClaimRunes = 20
	// minClaimWords is how many significant words a claim needs: fewer and it
	// is a fragment rather than a statement.
	minClaimWords = 3
	// claimLimit keeps one claim quotable.
	claimLimit = 400
	// fingerprintRunes is how much of a page decides whether it changed.
	fingerprintRunes = 4000
)

// normalizeQueries keeps the queries distinct and bounded, and falls back to
// the question itself so a plain sentence is enough to start.
func normalizeQueries(question string, queries []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, maxQueries)
	for _, query := range queries {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, trimmed)
		if len(result) == maxQueries {
			break
		}
	}
	if len(result) == 0 {
		if trimmed := strings.TrimSpace(question); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// bestPassage picks the passage of a page that answers the query, if the page
// has one. A page with nothing relevant contributes no claim: a passage that
// merely exists is not evidence.
func bestPassage(text string, queryTokens []string) (string, float64) {
	best := ""
	bestScore := 0.0
	for _, passage := range passages(text) {
		score := passageScore(passage, queryTokens)
		if score > bestScore {
			best = passage
			bestScore = score
		}
	}
	if bestScore < minPassageScore {
		return "", bestScore
	}
	return best, bestScore
}

// pageClaims turns the passage that answers the query into separate claims,
// one per sentence. Claims are what gets compared across pages: a paragraph
// compared as a whole would hide which of its sentences another source
// actually confirms, and no two sites word a paragraph identically.
func pageClaims(text string, queryTokens []string) []string {
	passage, _ := bestPassage(text, queryTokens)
	if passage == "" {
		return nil
	}
	claims := make([]string, 0, maxClaimsPerPage)
	for _, sentence := range splitSentences(passage) {
		sentence = strings.TrimSpace(sentence)
		// A claim is judged by its own weight: a short sentence that answers
		// the query ("Canberra is the capital of Australia.") is exactly the
		// kind of statement another site can confirm word for word.
		if len([]rune(sentence)) < minClaimRunes || len(domainresearch.Tokens(sentence)) < minClaimWords {
			continue
		}
		if passageScore(sentence, queryTokens) < minPassageScore {
			continue
		}
		claims = append(claims, truncateRunes(sentence, claimLimit))
		if len(claims) == maxClaimsPerPage {
			break
		}
	}
	if len(claims) == 0 {
		// The query words are spread over neighbouring sentences: the window
		// as a whole is still the answer, so it stays one claim.
		claims = append(claims, truncateRunes(passage, passageLimit))
	}
	return claims
}

// snippetClaims applies the same reading to a search snippet: a snippet is a
// lead rather than a page, but it is still evidence, and evidence is compared
// by claim. A snippet that cannot be split stays whole, because the search
// engine already extracted the part that answers the query.
func snippetClaims(snippet string, queryTokens []string) []string {
	claims := make([]string, 0, maxClaimsPerPage)
	for _, sentence := range splitSentences(snippet) {
		sentence = strings.TrimSpace(sentence)
		if len([]rune(sentence)) < minClaimRunes || len(domainresearch.Tokens(sentence)) < minClaimWords {
			continue
		}
		if passageScore(sentence, queryTokens) < minPassageScore {
			continue
		}
		claims = append(claims, truncateRunes(sentence, claimLimit))
		if len(claims) == maxClaimsPerPage {
			break
		}
	}
	if len(claims) == 0 {
		claims = append(claims, truncateRunes(snippet, maxSnippetRunes))
	}
	return claims
}

// passageScore is the share of query words the passage contains.
func passageScore(passage string, queryTokens []string) float64 {
	passageTokens := domainresearch.Tokens(passage)
	if len(passageTokens) == 0 {
		return 0
	}
	found := 0
	for _, wanted := range queryTokens {
		for _, have := range passageTokens {
			if domainresearch.SameWord(wanted, have) {
				found++
				break
			}
		}
	}
	if len(queryTokens) == 0 {
		return 0
	}
	return float64(found) / float64(len(queryTokens))
}

// passages splits readable text into windows of one to three sentences, which
// is how a claim still carries the context of the sentence before it.
func passages(text string) []string {
	result := make([]string, 0, 32)
	for _, paragraph := range paragraphs(text) {
		sentences := splitSentences(paragraph)
		for i := range sentences {
			window := sentences[i]
			if len([]rune(window)) < minPassageRunes && i+1 < len(sentences) {
				next := strings.TrimSpace(window + " " + sentences[i+1])
				if len([]rune(next)) <= passageLimit {
					window = next
				}
			}
			window = strings.TrimSpace(window)
			runes := len([]rune(window))
			if runes < minPassageRunes || runes > passageLimit*2 {
				continue
			}
			if runes > passageLimit {
				window = truncateRunes(window, passageLimit)
			}
			if !hasWords(window) {
				continue
			}
			result = append(result, window)
		}
	}
	return result
}

// paragraphs splits text on blank-ish lines and trims each block.
func paragraphs(text string) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	result := make([]string, 0, len(lines))
	buffer := make([]string, 0, 4)
	flush := func() {
		if len(buffer) == 0 {
			return
		}
		block := strings.Join(buffer, " ")
		block = strings.Join(strings.Fields(block), " ")
		if block != "" {
			result = append(result, block)
		}
		buffer = buffer[:0]
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		buffer = append(buffer, trimmed)
	}
	flush()
	return result
}

// splitSentences splits one paragraph into sentences. A sentence ends at a
// terminator followed by a space and an upper-case letter or a digit, so
// "3.5" and "example.com" do not split.
func splitSentences(paragraph string) []string {
	runes := []rune(paragraph)
	result := make([]string, 0, 8)
	start := 0
	for i := 0; i < len(runes); i++ {
		if !isTerminator(runes[i]) {
			continue
		}
		next := i + 1
		if next >= len(runes) {
			continue
		}
		if runes[next] != ' ' && runes[next] != '\u00a0' {
			continue
		}
		for next < len(runes) && (runes[next] == ' ' || runes[next] == '\u00a0') {
			next++
		}
		if next >= len(runes) {
			continue
		}
		if !unicode.IsUpper(runes[next]) && !unicode.IsDigit(runes[next]) && runes[next] != '«' && runes[next] != '"' {
			continue
		}
		sentence := strings.TrimSpace(string(runes[start : i+1]))
		if sentence != "" {
			result = append(result, sentence)
		}
		start = next
		i = next - 1
	}
	if start < len(runes) {
		if tail := strings.TrimSpace(string(runes[start:])); tail != "" {
			result = append(result, tail)
		}
	}
	if len(result) == 0 {
		if trimmed := strings.TrimSpace(paragraph); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func isTerminator(r rune) bool {
	return r == '.' || r == '!' || r == '?' || r == '…'
}

// hasWords reports whether the text contains at least one word of four
// letters, which rules out navigation leftovers and counters.
func hasWords(text string) bool {
	words := 0
	for _, field := range strings.Fields(text) {
		if runes := []rune(strings.TrimFunc(field, func(r rune) bool { return !unicode.IsLetter(r) })); len(runes) >= 4 {
			words++
		}
	}
	return words >= 5
}

// fingerprintOf reduces a page to what a watch compares next time.
func fingerprintOf(page Page, now time.Time) domainresearch.Fingerprint {
	text := page.Text
	if runes := []rune(text); len(runes) > fingerprintRunes {
		text = string(runes[:fingerprintRunes])
	}
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(text), " ")))
	return domainresearch.Fingerprint{
		URL:       domainresearch.CanonicalURL(page.URL),
		Title:     page.Title,
		Hash:      hex.EncodeToString(sum[:8]),
		FirstSeen: now,
	}
}

// buildSummary writes the report in the user's language: what is confirmed,
// what rests on one source, where the sources disagree, and how it was
// checked. The assistant may rewrite it afterwards; this text never claims
// more than the evidence shows.
func buildSummary(question string, queries []string, findings []domainresearch.Finding, pages int, at time.Time) string {
	var out strings.Builder
	fmt.Fprintf(&out, "## %s\n\n", question)
	fmt.Fprintf(&out, "Проверено %s. Страниц прочитано: %d.\n",
		at.In(time.UTC).Format("2 January 2006, 15:04 UTC"), pages)
	// A page that did not open still leaves a search snippet, and the reader
	// must not read "прочитано" as if every source had been read: the count of
	// pages and the number of sites behind a finding would look contradictory.
	if unread := countUnreadSources(findings); unread > 0 {
		fmt.Fprintf(&out, "Не удалось открыть источников: %d — они учтены по поисковой выдержке и помечены низким доверием.\n", unread)
	}

	facts := make([]domainresearch.Finding, 0, len(findings))
	assumptions := make([]domainresearch.Finding, 0, len(findings))
	snippets := make([]domainresearch.Finding, 0, len(findings))
	disputed := make([]domainresearch.Finding, 0, len(findings))
	for _, finding := range findings {
		switch {
		case finding.Conflict != "":
			disputed = append(disputed, finding)
		case finding.Certainty == domainresearch.CertaintyFact:
			facts = append(facts, finding)
		case !anyRead(finding):
			// No page behind this finding was opened: its sites are leads, not
			// reading, so it does not belong under "one source" either.
			snippets = append(snippets, finding)
		default:
			assumptions = append(assumptions, finding)
		}
	}

	if len(facts) > 0 {
		out.WriteString("\n### Подтверждено несколькими источниками\n\n")
		for _, finding := range facts {
			fmt.Fprintf(&out, "- %s — %s\n", finding.Statement, citationsLine(finding))
		}
	}
	if len(assumptions) > 0 {
		out.WriteString("\n### Один источник, требует проверки\n\n")
		for _, finding := range assumptions {
			fmt.Fprintf(&out, "- %s — %s\n", finding.Statement, citationsLine(finding))
		}
	}
	if len(snippets) > 0 {
		out.WriteString("\n### Только поисковая выдержка, страница не открылась\n\n")
		for _, finding := range snippets {
			fmt.Fprintf(&out, "- %s — %s\n", finding.Statement, citationsLine(finding))
		}
	}
	if len(disputed) > 0 {
		out.WriteString("\n### Источники расходятся\n\n")
		for _, finding := range disputed {
			fmt.Fprintf(&out, "- %s — %s\n  %s\n", finding.Statement, citationsLine(finding), finding.Conflict)
		}
	}
	if len(findings) == 0 {
		out.WriteString("\nSufficient evidence was not found: the pages did not contain a passage that answers the question.\n")
		out.WriteString("Достаточных оснований не найдено: страницы не содержали фрагмента, отвечающего на вопрос.\n")
	}
	if len(queries) > 0 {
		out.WriteString("\n### Как проверялось\n\n")
		fmt.Fprintf(&out, "Поисковые запросы: %s. Ни один вывод не считается фактом, если его подтверждает только один сайт.\n",
			strings.Join(queries, "; "))
	}
	return out.String()
}

// countUnreadSources is how many cited pages could not be read. Reading status
// is its own property: a low-trust source the user connected is still read, and
// a snippet from a respectable site is still only a snippet.
func countUnreadSources(findings []domainresearch.Finding) int {
	unread := map[string]bool{}
	for _, finding := range findings {
		for _, citation := range finding.Citations {
			if !citation.Read {
				unread[citation.URL] = true
			}
		}
	}
	return len(unread)
}

// citationsLine renders the sources of one finding as markdown links.
// anyRead reports whether at least one page behind a finding was opened. A
// finding built only from search snippets is not a source's claim: it is a
// lead that nobody has read yet.
func anyRead(finding domainresearch.Finding) bool {
	for _, citation := range finding.Citations {
		if citation.Read {
			return true
		}
	}
	return false
}

func citationsLine(finding domainresearch.Finding) string {
	parts := make([]string, 0, len(finding.Citations))
	seen := map[string]bool{}
	for _, citation := range finding.Citations {
		site := citation.Site
		if site == "" {
			site = citation.URL
		}
		if seen[citation.URL] {
			continue
		}
		seen[citation.URL] = true
		title := citation.Title
		if strings.TrimSpace(title) == "" {
			title = site
		}
		if citation.Read {
			parts = append(parts, fmt.Sprintf("[%s](%s)", title, citation.URL))
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s](%s) (выдержка)", title, citation.URL))
	}
	return strings.Join(parts, ", ")
}

// sourceSites lists the distinct sites behind the findings, for the knowledge
// entry's provenance label.
func sourceSites(findings []domainresearch.Finding) []string {
	seen := map[string]bool{}
	sites := make([]string, 0, 8)
	for _, finding := range findings {
		for _, site := range finding.Domains {
			if seen[site] {
				continue
			}
			seen[site] = true
			sites = append(sites, site)
		}
	}
	sort.Strings(sites)
	return sites
}

// countSources is the number of distinct pages cited by at least one finding.
func countSources(findings []domainresearch.Finding) int {
	seen := map[string]bool{}
	for _, finding := range findings {
		for _, citation := range finding.Citations {
			seen[citation.URL] = true
		}
	}
	return len(seen)
}

// knowledgeCertainty picks the level a saved report claims: a report is a fact
// only when at least one statement was corroborated, otherwise it is an
// assumption and the reader is told so.
func knowledgeCertainty(findings []domainresearch.Finding) string {
	for _, finding := range findings {
		if finding.Certainty == domainresearch.CertaintyFact {
			return string(domainresearch.CertaintyFact)
		}
	}
	return string(domainresearch.CertaintyAssumption)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// ensureValidRunes drops text that cannot be stored.
func ensureValidRunes(value string) string {
	if utf8.ValidString(value) {
		return value
	}
	return strings.ToValidUTF8(value, "")
}
