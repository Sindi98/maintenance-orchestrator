// Package ui serves a small, dependency-free web dashboard for the maintenance
// orchestrator. It renders server-side HTML (html/template, embedded) and uses
// only vanilla JS for live refresh — no Node/TypeScript build step. It reads and
// mutates MaintenanceRequests/MaintenancePolicies through the controller-runtime
// client, so it shares the manager's cache and credentials.
package ui

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Server is the built-in web dashboard. It implements manager.Runnable.
type Server struct {
	client client.Client
	addr   string
	log    logr.Logger
}

// NewServer constructs the dashboard server.
func NewServer(c client.Client, addr string, log logr.Logger) *Server {
	return &Server{client: c, addr: addr, log: log}
}

// NeedLeaderElection returns false so the dashboard runs on every replica: it
// only reads and patches API objects, which any instance may do.
func (s *Server) NeedLeaderElection() bool { return false }

// Start runs the HTTP server until ctx is cancelled (manager.Runnable).
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.routes(mux)

	srv := &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	s.log.Info("starting web dashboard", "addr", s.addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// routes wires the dashboard endpoints. Go 1.22 method+wildcard patterns are used.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", s.handleList)
	mux.HandleFunc("GET /new", s.handleNewForm)
	// State-changing POSTs are wrapped with a same-origin (CSRF) check.
	mux.HandleFunc("POST /requests", enforceSameOrigin(s.handleCreate))
	mux.HandleFunc("GET /requests/{name}", s.handleDetail)
	mux.HandleFunc("GET /requests/{name}/status", s.handleStatusFragment)
	mux.HandleFunc("POST /requests/{name}/approve", enforceSameOrigin(s.handleAction(actionApprove)))
	mux.HandleFunc("POST /requests/{name}/reject", enforceSameOrigin(s.handleAction(actionReject)))
	mux.HandleFunc("POST /requests/{name}/pause", enforceSameOrigin(s.handleAction(actionPause)))
	mux.HandleFunc("POST /requests/{name}/resume", enforceSameOrigin(s.handleAction(actionResume)))
	mux.HandleFunc("POST /requests/{name}/cancel", enforceSameOrigin(s.handleAction(actionCancel)))
	mux.HandleFunc("GET /fragment/requests", s.handleListFragment)
	mux.HandleFunc("GET /policies", s.handlePolicies)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticRoot))))
}
