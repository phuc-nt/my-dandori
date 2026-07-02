package config

import "strings"

// Pricing is USD per million tokens for one model.
type Pricing struct {
	Input      float64 `yaml:"input"`
	Output     float64 `yaml:"output"`
	CacheRead  float64 `yaml:"cache_read"`
	CacheWrite float64 `yaml:"cache_write"`
}

func defaultPricing() map[string]Pricing {
	return map[string]Pricing{
		"claude-fable-5":   {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
		"claude-opus-4-8":  {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
		"claude-sonnet-5":  {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
		"claude-haiku-4-5": {Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25},
		"default":          {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	}
}

// PriceFor returns the pricing entry whose key is a prefix of the model id,
// falling back to "default". Model ids carry suffixes like dates or "[1m]".
func (c *Config) PriceFor(model string) Pricing {
	if p, ok := c.Pricing[model]; ok {
		return p
	}
	for key, p := range c.Pricing {
		if key != "default" && strings.HasPrefix(model, key) {
			return p
		}
	}
	return c.Pricing["default"]
}

// Cost computes USD cost for a token usage set against a model's pricing.
func (c *Config) Cost(model string, input, output, cacheRead, cacheWrite int64) float64 {
	p := c.PriceFor(model)
	const m = 1_000_000
	return float64(input)*p.Input/m + float64(output)*p.Output/m +
		float64(cacheRead)*p.CacheRead/m + float64(cacheWrite)*p.CacheWrite/m
}
