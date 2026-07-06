package ingest

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/phuc-nt/dandori/internal/auth"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

// legacyPrincipal is the FIXED operator id attributed to every request
// authenticated via the pre-v10 shared Cfg.IngestToken (H1). It is never
// derived from the request — in particular never from X-Dandori-Principal,
// which a holder of the shared token could set to anything.
const legacyPrincipal = "legacy-shared@ingest"

// tokenPrefixHint mirrors auth's per-operator token prefix — used only to
// decide whether a fast-reject checksum failure should short-circuit auth
// (a legacy shared token need not carry this prefix).
const tokenPrefixHint = "dnd_"

type ctxKey int

const operatorIDCtxKey ctxKey = 0

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
	mux.HandleFunc("GET /ingest/context", s.handleContext)
	return s.auth(mux)
}

// auth dual-accepts two credential forms and never trusts a client-supplied
// principal (H1):
//
//  1. Per-operator token (Bearer dnd_...): SHA-256 lookup in api_tokens.
//     The row's operator_id becomes the request's principal.
//  2. Legacy shared token (Bearer == Cfg.IngestToken), only while
//     AllowLegacyIngestToken is true: principal is the FIXED constant
//     legacyPrincipal — never derived from any header.
//
// Anything else is 401. The bearer value is never logged.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		bearer, hasBearer := strings.CutPrefix(authz, "Bearer ")
		if !hasBearer || bearer == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		operatorID, ok := s.authenticate(bearer)
		if !ok {
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
		ctx := context.WithValue(r.Context(), operatorIDCtxKey, operatorID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authenticate resolves a bearer value to an operator id. Token branch is
// tried first; legacy branch only fires when the token branch misses AND
// legacy is still allowed.
func (s *Server) authenticate(bearer string) (operatorID string, ok bool) {
	// Fast-reject anything shaped like a per-operator token but with a
	// corrupted checksum (typo/truncation) before spending a DB round trip.
	// Legacy shared tokens don't necessarily match this shape, so a failed
	// checksum here does NOT rule out the legacy branch below.
	if strings.HasPrefix(bearer, tokenPrefixHint) && !auth.VerifyChecksum(bearer) {
		return "", false
	}
	hash := auth.TokenHash(bearer)
	if id, hit := s.St.LookupToken(hash); hit {
		_ = s.St.TouchToken(hash) // best-effort; never fail the request
		return id, true
	}

	if s.Cfg.AllowLegacyIngestToken && s.Cfg.IngestToken != "" {
		want := "Bearer " + s.Cfg.IngestToken
		got := "Bearer " + bearer
		if len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
			log.Println("ingest: deprecated shared token used — run `dandori token create <username>` for per-operator attribution")
			return legacyPrincipal, true
		}
	}

	return "", false
}

// operatorIDFromContext reads the principal auth() resolved server-side.
func operatorIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(operatorIDCtxKey).(string)
	return id
}

// handleEvents applies one batch in one transaction: run upserts, event
// inserts deduped by ULID, finalize numerics with SET semantics.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	var batch Batch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20)).Decode(&batch); err != nil {
		http.Error(w, "bad batch: "+err.Error(), http.StatusBadRequest)
		return
	}
	// operatorID comes from auth() (token lookup or fixed legacy principal)
	// — X-Dandori-Principal is NEVER read here for access control (H1). It
	// may still arrive on the wire as a diagnostic hint but this handler
	// ignores it entirely.
	operatorID := operatorIDFromContext(r.Context())
	if operatorID == "" {
		log.Println("ingest: handleEvents called without an authenticated operator in context")
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

// handleContext serves an agent's merged effective context for SessionStart
// injection on the dev machine. On a DB error it returns 500 (NOT 200-empty)
// so the client keeps its good stale cache rather than overwriting it with
// empty (M4). Any ingest-token holder can read any agent's context — single
// shared token, same posture as /ingest/policy (M3).
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	text, prov, err := contexthub.New(s.St).EffectiveContext(agent)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"context": text, "provenance": prov})
}
