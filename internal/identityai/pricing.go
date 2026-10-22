package identityai

import (
	"fmt"
	"regexp"
	"strings"
)

// Estimated cost before any paid call. Prices are USD per million tokens
// from the providers' public price lists on PricesAsOf; an account may be
// billed differently, and a model that is not listed gets no estimate (the
// page then says so and still requires approval).

// PricesAsOf is the date the table below was copied from the public lists.
const PricesAsOf = "2026-10-01"

type price struct{ input, output float64 }

// prices are keyed by exact model id; an id with a dated suffix
// (claude-sonnet-4-5-20250929, gpt-4o-2024-08-06) matches its base id.
// Sibling models (o3-pro, gpt-5-nano) never inherit a price: unlisted
// means unknown, and the page says so.
var prices = map[string]map[string]price{
	"anthropic": {
		"claude-opus-4-5":   {5, 25},
		"claude-opus-4-1":   {15, 75},
		"claude-opus-4":     {15, 75},
		"claude-sonnet-4-5": {3, 15},
		"claude-sonnet-4":   {3, 15},
		"claude-haiku-4-5":  {1, 5},
	},
	"openai": {
		"gpt-4o":       {2.5, 10},
		"gpt-4o-mini":  {0.15, 0.6},
		"gpt-4.1":      {2, 8},
		"gpt-4.1-mini": {0.4, 1.6},
		"gpt-4.1-nano": {0.1, 0.4},
		"gpt-5":        {1.25, 10},
		"gpt-5-mini":   {0.25, 2},
		"gpt-5-nano":   {0.05, 0.4},
		"o3":           {2, 8},
		"o4-mini":      {1.1, 4.4},
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
	est.PerMillionInput, est.PerMillionOutput = p.input, p.output
	est.USD = float64(inputTokens)*p.input/1e6 + float64(outputTokens)*p.output/1e6
	return est
}

// estimateTokens is the usual rough rule for Latin and Arabic prompts.
func estimateTokens(s string) int { return len([]rune(s))/3 + 1 }
