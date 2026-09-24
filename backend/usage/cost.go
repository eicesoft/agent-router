package usage

// PriceFunc looks up a route's unit prices (USD per 1M tokens) from the model
// mappings. Missing routes return zeros, so their usage bills at $0.
type PriceFunc func(providerID, clientModel string) (input, output, cacheRead float64)

// costOf applies the billing formula to one route's token totals:
//
//	input  = (input - cached) * inputPrice / 1e6
//	output = output * outputPrice / 1e6
//	cache  = cached * cacheReadPrice / 1e6
//	total  = input + output + cache
//
// cached is clamped to input so a malformed row cannot produce a negative
// uncached count.
func costOf(price PriceFunc, providerID, clientModel string, input, output, cached int) (inCost, outCost, cacheCost, total float64) {
	if price == nil {
		return 0, 0, 0, 0
	}
	if cached > input {
		cached = input
	}
	inPrice, outPrice, cachePrice := price(providerID, clientModel)
	inCost = float64(input-cached) * inPrice / 1e6
	outCost = float64(output) * outPrice / 1e6
	cacheCost = float64(cached) * cachePrice / 1e6
	return inCost, outCost, cacheCost, inCost + outCost + cacheCost
}

// addCost folds one route's cost into a dimension total.
func addCost(stat *UsageStat, price PriceFunc, providerID, clientModel string, input, output, cached int) {
	inCost, outCost, cacheCost, total := costOf(price, providerID, clientModel, input, output, cached)
	stat.InputCost += inCost
	stat.OutputCost += outCost
	stat.CacheCost += cacheCost
	stat.TotalCost += total
}

// addToCost folds one route's cost into a request-log stats row (same fields,
// different struct so the log panel does not carry UsageStat's breakdown keys).
func addToCost(stats *RequestLogStats, price PriceFunc, providerID, clientModel string, input, output, cached int) {
	inCost, outCost, cacheCost, total := costOf(price, providerID, clientModel, input, output, cached)
	stats.InputCost += inCost
	stats.OutputCost += outCost
	stats.CacheCost += cacheCost
	stats.TotalCost += total
}
