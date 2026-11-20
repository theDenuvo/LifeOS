package skills

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	domainskill "github.com/theDenuvo/LifeOS/internal/domain/skill"
)

// Selection is deterministic on purpose: the same query and the same
// library always connect the same skills, with the matched terms kept so
// the choice can be explained and journalled. The model still decides
// what to do with the connected instructions; selection only decides
// which instructions are worth its attention.

const (
	maxSelectedSkills   = 3
	maxSelectedRunes    = 12000
	selectionScoreFloor = 3
)

// stopwords never carry a skill match. They are the words every Russian
// and English request shares, so counting them would connect skills to
// almost any turn.
var stopwords = map[string]struct{}{
	"в": {}, "и": {}, "на": {}, "с": {}, "по": {}, "для": {}, "что": {}, "это": {},
	"как": {}, "не": {}, "то": {}, "а": {}, "но": {}, "или": {}, "к": {}, "у": {},
	"от": {}, "до": {}, "из": {}, "за": {}, "при": {}, "про": {}, "бы": {}, "же": {},
	"the": {}, "and": {}, "for": {}, "with": {}, "this": {}, "that": {}, "from": {},
	"about": {}, "when": {}, "what": {}, "how": {}, "can": {}, "you": {},
}

// tokens splits text into significant lower-cased terms.
func tokens(text string) []string {
	lowered := strings.ToLower(text)
	var b strings.Builder
	for _, r := range lowered {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	fields := strings.Fields(b.String())
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		if len([]rune(field)) < 3 {
			continue
		}
		if _, ok := stopwords[field]; ok {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

// sameTerm reports whether two terms are word forms of one word. Russian
// endings change the tail, so the comparison uses a shared head of at
// least four runes instead of exact equality — while a shared head alone
// (a short word that is a prefix of a longer, different word) still needs
// the shorter term to be at least four runes long.
func sameTerm(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	short, long := ra, rb
	if len(rb) < len(ra) {
		short, long = rb, ra
	}
	if len(short) < 4 {
		return false
	}
	head := 5
	if len(short) < head {
		head = len(short)
	}
	for i := 0; i < head; i++ {
		if short[i] != long[i] {
			return false
		}
	}
	// The shared head must be the whole shorter word or a real stem: two
	// different words that merely start alike (e.g. a 4-rune prefix of a
	// much longer word that continues differently after rune 5) still
	// share the head, so require the 5th rune to also match when both
	// words are longer than the head.
	if len(short) > head && len(long) > head && short[head] != long[head] {
		return false
	}
	return true
}

func matchesAny(query []string, terms []string) []string {
	var matched []string
	for _, q := range query {
		for _, term := range terms {
			if sameTerm(q, term) {
				matched = append(matched, q)
				break
			}
		}
	}
	return matched
}

// scoreSkill weights name and tags highest (they are the author's own
// conditions), then the conditions text, then the body. A single generic
// word in a long instruction is not enough to connect a skill.
func scoreSkill(query string, skill *domainskill.Skill) (float64, []string, bool) {
	if isExcluded(query, skill.Name) {
		return 0, nil, true
	}
	q := tokens(query)
	if len(q) == 0 {
		return 0, nil, false
	}
	content := skill.Content()
	var score float64
	var matched []string
	add := func(text string, weight float64) {
		m := matchesAny(q, tokens(text))
		if len(m) > 0 {
			score += weight * float64(len(m))
			for _, term := range m {
				if !containsString(matched, term) {
					matched = append(matched, term)
				}
			}
		}
	}
	add(content.Name, 3)
	add(strings.Join(content.Tags, " "), 3)
	add(content.WhenToUse, 2)
	add(content.Purpose, 1)
	add(content.Instructions, 0.5)
	add(content.Limitations, 0.5)
	add(content.Examples, 0.5)

	// An explicit mention of the skill by name is the strongest signal:
	// the user (or the analysis scenario) named it directly.
	if strings.Contains(strings.ToLower(query), domainskill.NormalizeName(skill.Name)) {
		score += 10
		if !containsString(matched, "название") {
			matched = append(matched, "название")
		}
	}
	sort.Strings(matched)
	return score, matched, false
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// isExcluded honours an explicit "do not apply this skill" in the same
// turn: a negation close before the skill name removes it from selection,
// whatever its keyword score would have been.
func isExcluded(query, name string) bool {
	lowered := strings.ToLower(query)
	needle := domainskill.NormalizeName(name)
	idx := strings.Index(lowered, needle)
	if idx < 0 {
		return false
	}
	window := lowered[max(0, idx-40):idx]
	for _, phrase := range []string{"не применяй", "не используй", "не нужен", "не нужно", "без навыка", "не применяй навык", "не используй навык"} {
		if strings.Contains(window, phrase) {
			return true
		}
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// selectSkills ranks active skills for one query and returns at most
// maxSelectedSkills whose total instructions fit the prompt budget.
func selectSkills(query string, skills []*domainskill.Skill) []selectedSkill {
	type scored struct {
		skill   *domainskill.Skill
		score   float64
		matched []string
	}
	var ranked []scored
	for _, skill := range skills {
		if skill == nil || !skill.Status.Injected() {
			continue
		}
		score, matched, excluded := scoreSkill(query, skill)
		if excluded || score < selectionScoreFloor {
			continue
		}
		ranked = append(ranked, scored{skill: skill, score: score, matched: matched})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].skill.Name < ranked[j].skill.Name
	})
	var out []selectedSkill
	budget := maxSelectedRunes
	for _, item := range ranked {
		if len(out) >= maxSelectedSkills {
			break
		}
		size := len([]rune(item.skill.Instructions))
		// The budget is a hard cap: a single skill whose instructions
		// exceed it is skipped, never injected oversized.
		if size > budget {
			continue
		}
		budget -= size
		out = append(out, selectedSkill{Skill: item.skill, Score: item.score, Matched: item.matched})
	}
	return out
}

type selectedSkill struct {
	Skill   *domainskill.Skill
	Score   float64
	Matched []string
}

// runTestScenarios checks a proposed body against its own scenarios: for
// each scenario the new body must not score worse than the current one.
// It is a selection check, not a model run — it proves the new text still
// connects where it should, before the user applies it.
func runTestScenarios(current, proposed domainskill.Content, scenarios []string) string {
	if len(scenarios) == 0 {
		return "тестовых сценариев нет; проверена только допустимость текста"
	}
	better, equal, worse := 0, 0, 0
	for _, scenario := range scenarios {
		currentSkill := tempSkill(current)
		proposedSkill := tempSkill(proposed)
		oldScore, _, _ := scoreSkill(scenario, currentSkill)
		newScore, _, _ := scoreSkill(scenario, proposedSkill)
		switch {
		case newScore > oldScore:
			better++
		case newScore < oldScore:
			worse++
		default:
			equal++
		}
	}
	summary := fmt.Sprintf("сценариев %d: лучше %d, так же %d, хуже %d", len(scenarios), better, equal, worse)
	if worse > 0 {
		return summary + " — новый вариант хуже старого хотя бы на одном сценарии"
	}
	return summary + " — новый вариант не хуже старого"
}

func tempSkill(content domainskill.Content) *domainskill.Skill {
	return &domainskill.Skill{
		Name: content.Name, Purpose: content.Purpose, WhenToUse: content.WhenToUse,
		Instructions: content.Instructions, Limitations: content.Limitations,
		Examples: content.Examples, Tags: content.Tags, Status: domainskill.StatusActive,
	}
}
