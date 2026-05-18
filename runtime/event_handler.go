package runtime

import (
	"fmt"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/state"
)

func (e *Engine) handleInputUpdate(ev core.Event) {
	if e.Probability == nil {
		e.publishRisk("probability model is nil")
		return
	}
	if len(e.Strategies) == 0 {
		e.publishRisk("strategy model is nil")
		return
	}

	obs, ok := e.Probability.OnUpdate(ev)
	if !ok {
		return
	}
	e.ticks.Add(1)

	var snap state.Snapshot
	hasSnap := false
	for _, strategy := range e.Strategies {
		if strategy == nil {
			continue
		}
		if !hasSnap {
			snap = e.State.Snapshot()
			hasSnap = true
		}
		intents := strategy.OnUpdate(ev, obs, snap)
		if len(intents) == 0 {
			continue
		}
		if !e.submitIntents(intents, snap, buildMidPrices(obs)) {
			return
		}
	}
}

func (e *Engine) handleExecutionAwareStrategy(data core.ExecutionEvent) {
	if e.Probability == nil {
		e.publishRisk("probability model is nil")
		return
	}

	if len(e.Strategies) == 0 {
		e.publishRisk("strategy model is nil")
		return
	}

	obs, ok := e.currentObservation()
	if !ok {
		return
	}

	var snap state.Snapshot
	hasSnap := false
	for _, s := range e.Strategies {
		strategy, ok := s.(ExecutionAwareStrategy)
		if !ok {
			continue
		}
		if !hasSnap {
			snap = e.State.Snapshot()
			hasSnap = true
		}

		intents := strategy.OnExecution(data, obs, snap)
		if len(intents) == 0 {
			continue
		}
		if !e.submitIntents(intents, snap, buildMidPrices(obs)) {
			return
		}
	}
}

func (e *Engine) handleStrategyTick(now time.Time) {
	if len(e.Strategies) == 0 {
		return
	}

	obs, ok := e.currentObservation()
	if !ok {
		return
	}

	snap := e.State.Snapshot()
	for _, s := range e.Strategies {
		tickStrategy, ok := s.(TickStrategy)
		if !ok {
			continue
		}
		intents := tickStrategy.OnTick(now, obs, snap)
		if len(intents) == 0 {
			continue
		}
		if !e.submitIntents(intents, snap, buildMidPrices(obs)) {
			return
		}
	}
}

func (e *Engine) currentObservation() (Observation, bool) {
	if e.Probability == nil {
		return Observation{}, false
	}
	provider, ok := e.Probability.(ProbabilitySnapshotProvider)
	if !ok {
		return Observation{}, false
	}
	return provider.CurrentObservation()
}

// buildMidPrices extracts (Ask+Bid)/2 per token from an Observation. Tokens
// with zero or missing prices are skipped — Risk.Check tolerates a partial map.
func buildMidPrices(obs Observation) map[string]float64 {
	mids := make(map[string]float64, len(obs.Tokens))
	for _, tk := range obs.Tokens {
		if tk.AskPrice > 0 && tk.BidPrice > 0 {
			mids[tk.Id] = (tk.AskPrice + tk.BidPrice) / 2
		}
	}
	return mids
}

func (e *Engine) submitIntents(intents []OrderIntent, snap state.Snapshot, midPrices map[string]float64) bool {
	if len(intents) == 0 {
		return true
	}

	if err := e.Risk.Check(intents, snap, midPrices); err != nil {
		e.riskRejected.Add(1)
		e.publishRisk(err.Error())
		return false
	}

	submit := make([]OrderIntent, 0, len(intents))
	now := time.Now()
	for _, in := range intents {
		action := in.Action
		if action == "" {
			action = OrderIntentActionPlace
			in.Action = action
		}
		if action != OrderIntentActionPlace {
			submit = append(submit, in)
			continue
		}
		if in.IntentID == "" {
			in.IntentID = e.nextIntentID()
		}
		if err := e.State.TryReserveProvisional(in.IntentID, in.MarketID, in.TokenID, in.Side, in.Price, in.Size, now, e.ProvisionalOrderTTL); err != nil {
			e.riskRejected.Add(1)
			e.publishRisk(fmt.Sprintf("provisional reserve failed intent=%s reason=%s", in.IntentID, err.Error()))
			continue
		}
		submit = append(submit, in)
	}
	if len(submit) == 0 {
		return true
	}

	e.ordersSent.Add(uint64(len(submit)))
	e.Exec.Execute(submit)
	return true
}
