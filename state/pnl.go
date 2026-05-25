package state

// UnrealizedPnL returns the mark-to-market PnL on currently-held available
// positions. Skips tokens with AvgCostKnown=false (e.g., positions imported
// via reconcile without a buy history).
//
// midPrices is tokenID → current mid price (typically (Ask+Bid)/2). Tokens
// without a mid entry are skipped.
func (s *State) UnrealizedPnL(midPrices map[string]float64) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pnl float64
	for tokenID, tp := range s.position.Tokens {
		if !tp.AvgCostKnown {
			continue
		}
		mid, ok := midPrices[tokenID]
		if !ok || tp.Available <= 0 {
			continue
		}
		pnl += (mid - tp.AvgCost) * tp.Available
	}
	return pnl
}
