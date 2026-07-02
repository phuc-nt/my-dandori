package ingest

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

// Server is the central-mode listener. It is a SEPARATE http.Server from the
// console (red-team C1/C2): routable bind, bearer-token auth on every route,
// and NO console routes — the unauthenticated console stays on 127.0.0.1.
type Server struct {
	Cfg *config.Config
	St  *store.Store
	sem chan struct{} // bounded in-flight requests (goroutine pile-up guard)
}

func NewServer(cfg *config.Config, st *store.Store) *Server {
	return &Server{Cfg: cfg, St: st, sem: make(chan struct{}, 8)}
}

// ListenAndServe blocks serving the ingest API on cfg.IngestListen.
func (s *Server) ListenAndServe() error {
	srv := &http.Server{
		Addr:         s.Cfg.IngestListen,
		Handler:      s.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return srv.ListenAndServe()
}

// Handler builds the token-authed ingest mux (exported for tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ingest/events", s.handleEvents)
	mux.HandleFunc("GET /ingest/policy", s.handlePolicy)
	return s.auth(mux)
}

// auth verifies the bearer token with a constant-time compare. The token is
// never logged (red-team L2).
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Cfg.IngestToken == "" {
			http.Error(w, "ingest disabled", http.StatusServiceUnavailable)
			return
		}
		got := r.Header.Get("Authorization")
		want := "Bearer " + s.Cfg.IngestToken
		if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleEvents applies one batch in one transaction: run upserts, event
// inserts deduped by ULID, finalize numerics with SET semantics.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	var batch Batch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20)).Decode(&batch); err != nil {
		http.Error(w, "bad batch: "+err.Error(), http.StatusBadRequest)
		return
	}
	principal := r.Header.Get("X-Dandori-Principal")
	operatorID, err := s.St.ResolveOperator(principal)
	if err != nil {
		log.Println("ingest: resolve operator:", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	applied, err := s.applyBatch(operatorID, batch.Records)
	if err != nil {
		log.Println("ingest: apply batch:", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"applied": applied})
}

// handlePolicy serves the governance snapshot for local pre-tool evaluation.
func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	snap, err := govern.BuildPolicySnapshot(s.St, s.Cfg.Budget.GlobalMonthlyUSD)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}
