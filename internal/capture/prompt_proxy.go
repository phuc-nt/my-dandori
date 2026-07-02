package capture

import (
	"fmt"
	"regexp"
	"strings"
)

// Prompt proxy: numeric signals about the opening prompt, computed on the
// machine that holds the transcript. ONLY these numbers are stored or
// transmitted — never the prompt text itself. Coarse by design: presence
// flags, not scores (Vietnamese and English prompts both work).

var (
	pathLikeRe = regexp.MustCompile(`[\w./-]+\.(go|ts|js|py|md|sql|html|yaml|yml|json)\b`)
	taskRefRe  = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b|#\d+\b`)
	criteriaRe = regexp.MustCompile(`(?i)acceptance|criteria|must|phải|yêu cầu|khi xong|definition of done`)
)

// Specificity bit flags — a prompt that names files, references a task and
// states done-criteria is the "planning-first" pattern top performers show.
const (
	SpecHasPath     = 1 << iota // names a concrete file
	SpecHasTaskRef              // references an issue/PR
	SpecHasCriteria             // states acceptance criteria
)

// PromptProxy reduces the first user prompt to (word count, specificity
// flags). Pure function; callers persist the numbers as a prompt_proxy event.
func PromptProxy(firstUser string) (words, spec int) {
	words = len(strings.Fields(firstUser))
	if pathLikeRe.MatchString(firstUser) {
		spec |= SpecHasPath
	}
	if taskRefRe.MatchString(firstUser) {
		spec |= SpecHasTaskRef
	}
	if criteriaRe.MatchString(firstUser) {
		spec |= SpecHasCriteria
	}
	return words, spec
}

// PromptProxyPayload is the compact numeric JSON stored in the event.
func PromptProxyPayload(words, spec int) string {
	return fmt.Sprintf(`{"w":%d,"spec":%d}`, words, spec)
}
