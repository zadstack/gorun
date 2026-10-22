package identityai

import (
	"fmt"
	"strings"
)

// Estimated cost before any paid call. Prices are USD per million tokens
// from the providers' public price lists on PricesAsOf; an account may be
// billed differently, and a model that is not listed gets no estimate (the
// page then says so and still requires approval).

// PricesAsOf is the date the table below was copied from the public lists.
const PricesAsOf = "2026-10-01"

type price struct{ input, output float64 }

// prices are matched by the longest model-id prefix per provider.
var prices = map[string]map[string]price{
	"anthropic": {
		"claude-opus":   {15, 75},
		"claude-sonnet": {3, 15},
		"claude-haiku":  {0.8, 4},
	},
	"openai": {
		"gpt-4o-mini": {0.15, 0.6},
		"gpt-4o":      {2.5, 10},
		"gpt-4.1":     {2, 8},
		"gpt-5":       {1.25, 10},
		"o4-mini":     {1.1, 4.4},
		"o3":          {2, 8},
	},
}

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
	best, bestLen := price{}, -1
	for prefix, p := range prices[strings.ToLower(provider)] {
		if strings.HasPrefix(strings.ToLower(model), prefix) && len(prefix) > bestLen {
			best, bestLen = p, len(prefix)
		}
	}
	if bestLen < 0 {
		return est
	}
	est.Known = true
	est.PerMillionInput, est.PerMillionOutput = best.input, best.output
	est.USD = float64(inputTokens)*best.input/1e6 + float64(outputTokens)*best.output/1e6
	return est
}

// estimateTokens is the usual rough rule for Latin and Arabic prompts.
func estimateTokens(s string) int { return len([]rune(s))/3 + 1 }
