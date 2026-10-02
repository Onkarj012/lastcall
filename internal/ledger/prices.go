package ledger

import "strings"

// Price is USD per 1M tokens at standard API rates.
type Price struct {
	In, CacheRead, CacheWrite5m, CacheWrite1h, Out float64
	// OpenAI bills a whole request at higher rates once input passes a threshold.
	LongAbove       int64
	LongIn, LongOut float64 // multipliers
}

// Sources, read 2026-10-03:
//
//	Anthropic: claude-api reference (cached 2026-09-25). Cache writes priced at the standard
//	1.25x (5m) / 2x (1h) of input; cache reads as listed, else 0.1x input.
//	OpenAI: developers.openai.com/api/docs/pricing and model pages. Cache writes 1.25x input;
//	prompts over 272K input tokens cost 2x input/cache and 1.5x output for the whole request.
var prices = map[string]Price{
	"claude-fable-5-1":  claude(10, 50, 0.25),
	"claude-fable-5":    claude(10, 50, 1.00),
	"claude-opus-5-5":   claude(4, 20, 0.20),
	"claude-opus-5":     claude(5, 25, 0.50),
	"claude-opus-4-8":   claude(5, 25, 0.50),
	"claude-sonnet-5-5": claude(2, 10, 0.20),
	"claude-sonnet-5":   claude(2, 10, 0.20),
	"claude-sonnet-4-6": claude(3, 15, 0.30),
	"claude-haiku-4-5":  claude(1, 5, 0.10),

	"gpt-6-astra":   openai(10, 1.00, 50),
	"gpt-6.1-sol":   openai(2, 0.10, 10),
	"gpt-6-sol":     openai(2, 0.20, 10),
	"gpt-6-luna":    openai(0.10, 0.01, 0.50),
	"gpt-5.6-sol":   openai(4, 0.40, 20),
	"gpt-5.6-terra": openai(2, 0.20, 12),
	"gpt-5.6-luna":  openai(0.20, 0.02, 1.20),
}

func claude(in, out, read float64) Price {
	return Price{In: in, Out: out, CacheRead: read, CacheWrite5m: in * 1.25, CacheWrite1h: in * 2}
}

func openai(in, cached, out float64) Price {
	return Price{In: in, Out: out, CacheRead: cached, CacheWrite5m: in * 1.25, CacheWrite1h: in * 1.25,
		LongAbove: 272_000, LongIn: 2, LongOut: 1.5}
}

// lookup matches exact ids, then strips date suffixes like claude-haiku-4-5-20251001.
func lookup(model string) (Price, bool) {
	m := strings.ToLower(model)
	if p, ok := prices[m]; ok {
		return p, true
	}
	for len(m) > 0 {
		i := strings.LastIndex(m, "-")
		if i < 0 {
			break
		}
		m = m[:i]
		if p, ok := prices[m]; ok {
			return p, true
		}
	}
	return Price{}, false
}

// cost prices one response. in is uncached input; all counts are tokens.
func (p Price) cost(in, read, w5, w1h, out int64) float64 {
	inM, outM := 1.0, 1.0
	if p.LongAbove > 0 && in+read+w5+w1h > p.LongAbove {
		inM, outM = p.LongIn, p.LongOut
	}
	return (float64(in)*p.In*inM + float64(read)*p.CacheRead*inM + float64(w5)*p.CacheWrite5m*inM +
		float64(w1h)*p.CacheWrite1h*inM + float64(out)*p.Out*outM) / 1e6
}
