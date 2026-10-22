package identityai

import (
	"fmt"
	"regexp"
	"strings"
)

// Estimated cost before any paid call. Prices are USD per million tokens
// copied from the providers' public price lists on PricesAsOf; an account
// may be billed differently, and a model that is not listed gets no
// estimate (the page then says so and still requires approval).

// PricesAsOf is the date the table below was copied from the public lists.
const PricesAsOf = "2026-10-22"

// Sources of the rows below.
const (
	sourceAnthropic = "https://platform.claude.com/docs/en/about-claude/pricing"
	sourceOpenAI    = "https://openai.com/api/pricing"
)

type price struct {
	input, output float64
	source        string
}

// prices are keyed by exact model id; an id with a dated suffix
// (claude-sonnet-4-5-20250929, gpt-4o-2024-08-06) matches its base id.
// Sibling models never inherit a price: unlisted means unknown, and the
// page says so. OpenAI reasoning models (gpt-5*, o-series) are deliberately
// absent: they require max_completion_tokens on chat/completions, which
// the shared client does not send, so a call would be rejected anyway.
var prices = map[string]map[string]price{
	"anthropic": {
		"claude-opus-5-5":   {4, 20, sourceAnthropic},
		"claude-opus-5":     {5, 25, sourceAnthropic},
		"claude-opus-4-8":   {5, 25, sourceAnthropic},
		"claude-opus-4-7":   {5, 25, sourceAnthropic},
		"claude-opus-4-6":   {5, 25, sourceAnthropic},
		"claude-opus-4-5":   {5, 25, sourceAnthropic},
		"claude-opus-4-1":   {15, 75, sourceAnthropic},
		"claude-opus-4":     {15, 75, sourceAnthropic},
		"claude-sonnet-5-5": {2, 10, sourceAnthropic},
		"claude-sonnet-5":   {2, 10, sourceAnthropic},
		"claude-sonnet-4-6": {3, 15, sourceAnthropic},
		"claude-sonnet-4-5": {3, 15, sourceAnthropic},
		"claude-sonnet-4":   {3, 15, sourceAnthropic},
		"claude-haiku-5-5":  {0.10, 0.50, sourceAnthropic}, // rate for prompts up to 100k tokens; ours are far smaller
		"claude-haiku-4-5":  {1, 5, sourceAnthropic},
	},
	"openai": {
		"gpt-4o":       {2.5, 10, sourceOpenAI},
		"gpt-4o-mini":  {0.15, 0.6, sourceOpenAI},
		"gpt-4.1":      {2, 8, sourceOpenAI},
		"gpt-4.1-mini": {0.4, 1.6, sourceOpenAI},
		"gpt-4.1-nano": {0.1, 0.4, sourceOpenAI},
	},
}

var datedSuffix = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)

// Estimate is the cost of one generation.
type Estimate struct {
	Known            bool
	USD              float64
	InputTokens      int
	OutputTokens     int
	AsOf             string
	Source           string
	PerMillionInput  float64
	PerMillionOutput float64
}

// Text formats the estimate for the page ("≈ $0.0043").
func (e Estimate) Text() string {
	if !e.Known {
		return ""
	}
	return fmt.Sprintf("≈ $%.4f", e.USD)
}

// EstimateCost looks up provider/model and prices inputTokens + outputTokens.
func EstimateCost(provider, model string, inputTokens, outputTokens int) Estimate {
	est := Estimate{InputTokens: inputTokens, OutputTokens: outputTokens, AsOf: PricesAsOf}
	id := datedSuffix.ReplaceAllString(strings.ToLower(strings.TrimSpace(model)), "")
	p, ok := prices[strings.ToLower(provider)][id]
	if !ok {
		return est
	}
	est.Known = true
	est.Source = p.source
	est.PerMillionInput, est.PerMillionOutput = p.input, p.output
	est.USD = float64(inputTokens)*p.input/1e6 + float64(outputTokens)*p.output/1e6
	return est
}

// estimateTokens is a rough rule (one token per three characters); Arabic
// usually takes more tokens per character, so the page calls it rough.
func estimateTokens(s string) int { return len([]rune(s))/3 + 1 }
