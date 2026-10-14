//go:build metrics

package metrics

import (
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The lifetime bound on the labels of the llm families: this many providers
// and named (provider, model) pairs, whatever happens to the configuration.
// Everything else is counted under otherLabel.
const (
	maxProviders  = 16
	maxModelPairs = 64
	otherLabel    = "other"
)

// budget decides the provider and model labels of an attempt and holds the
// bound on them. A name it admitted stays admitted, so a reload never takes a
// series away from a counter, and the series count stays bounded however often
// the configuration is rewritten. A restart starts a fresh budget.
type budget struct {
	mu        sync.Mutex
	providers map[string]struct{}
	pairs     map[[2]string]struct{}
}

func newBudget() *budget {
	return &budget{providers: map[string]struct{}{}, pairs: map[[2]string]struct{}{}}
}

// labels folds the provider row and the API model id of an attempt against
// cfg: a name the configuration declares and the budget admits (or has
// admitted) stands for itself, anything else is other. The model is other
// under the provider other.
func (b *budget) labels(cfg *config.Config, provider, model string) (string, string) {
	if cfg == nil || provider == "" || cfg.FindProvider(provider) == nil {
		return otherLabel, otherLabel
	}
	declared := cfg.FindModelEntry(provider+"/"+model) != nil
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.providers[provider]; !ok {
		if len(b.providers) >= maxProviders {
			return otherLabel, otherLabel
		}
		b.providers[provider] = struct{}{}
	}
	if !declared {
		return provider, otherLabel
	}
	pair := [2]string{provider, model}
	if _, ok := b.pairs[pair]; !ok {
		if len(b.pairs) >= maxModelPairs {
			return provider, otherLabel
		}
		b.pairs[pair] = struct{}{}
	}
	return provider, model
}
