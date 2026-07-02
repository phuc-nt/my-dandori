package cli

import (
	"context"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// startWorkers launches background goroutines for serve: transcript watcher,
// jira sync, alert dispatcher, approval poller. Phases 2 and 6 register here.
func startWorkers(ctx context.Context, cfg *config.Config, st *store.Store) {
	for _, w := range workerRegistry {
		go w(ctx, cfg, st)
	}
}

type workerFunc func(context.Context, *config.Config, *store.Store)

var workerRegistry []workerFunc

func registerWorker(w workerFunc) { workerRegistry = append(workerRegistry, w) }
