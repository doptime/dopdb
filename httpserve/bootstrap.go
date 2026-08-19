package httpserve

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/config"
)

// Serve is the one-line bootstrap. Given a loaded config it connects every
// [[kvrocks]] datasource, installs them on dopdb, builds the HTTP handler,
// applies CORS, and listens. Register your collections (dopdb.RegisterHttp) and grant
// permissions before calling, or pass a configured permission set with
// WithPermissions:
//
//	cfg, _ := config.Load("config.toml")
//	perms := httpserve.NewPermissions()
//	perms.Grant("HGET", "User"); perms.Grant("HSET", "User")
//	log.Fatal(httpserve.Serve(cfg, httpserve.WithPermissions(perms)))
//
// Datasource selection per request is by ?ds=<name> (default "default").
func Serve(cfg *config.Config, opts ...ServeOption) error {
	ctx := context.Background()

	sources := make([]dopdb.DatasourceConfig, 0, len(cfg.Kvrocks))
	for _, k := range cfg.Kvrocks {
		sources = append(sources, dopdb.DatasourceConfig{
			Name:      k.Name,
			URI:       k.URI,
			Namespace: k.Namespace,
			Password:  k.Password,
		})
	}
	ds, err := dopdb.ConnectDatasources(ctx, sources)
	if err != nil {
		return err
	}
	dopdb.SetDatasources(ds)

	o := &serveOptions{perms: NewPermissions()}
	for _, opt := range opts {
		opt(o)
	}

	h := NewHandler(NewServer(cfg.HTTP.JWTSecret), o.perms)

	var handler http.Handler = h
	if len(cfg.HTTP.CORSOrigins) > 0 {
		handler = withCORS(handler, cfg.HTTP.CORSOrigins)
	}
	return http.ListenAndServe(cfg.HTTP.Addr, handler)
}

// ServeHandle is what ServeWithHandle returns: the running HTTP server and a
// Close function that shuts it down gracefully (drains connections, releases
// listeners). Callers use this instead of Serve when they need lifecycle control.
type ServeHandle struct {
	Server *http.Server
	Close  func(ctx context.Context) error
	// ListenErr receives a listener failure that happens after startup, and is
	// closed when the server stops. Reading it is optional.
	ListenErr <-chan error
}

// ServeWithHandle is like Serve but returns a *ServeHandle so the caller can
// shut the server down gracefully. The Serve signature is kept for backward
// compatibility (it delegates here and blocks on ListenAndServe).
//
//	srv, err := httpserve.ServeWithHandle(cfg, httpserve.WithPermissions(perms))
//	// ... later ...
//	srv.Close(context.Background())
func ServeWithHandle(cfg *config.Config, opts ...ServeOption) (*ServeHandle, error) {
	ctx := context.Background()

	sources := make([]dopdb.DatasourceConfig, 0, len(cfg.Kvrocks))
	for _, k := range cfg.Kvrocks {
		sources = append(sources, dopdb.DatasourceConfig{
			Name:      k.Name,
			URI:       k.URI,
			Namespace: k.Namespace,
			Password:  k.Password,
		})
	}
	ds, err := dopdb.ConnectDatasources(ctx, sources)
	if err != nil {
		return nil, err
	}
	dopdb.SetDatasources(ds)

	o := &serveOptions{perms: NewPermissions()}
	for _, opt := range opts {
		opt(o)
	}

	h := NewHandler(NewServer(cfg.HTTP.JWTSecret), o.perms)

	var handler http.Handler = h
	if len(cfg.HTTP.CORSOrigins) > 0 {
		handler = withCORS(handler, cfg.HTTP.CORSOrigins)
	}

	// Timeouts. Without ReadHeaderTimeout a client that opens a connection and
	// never finishes its headers holds a file descriptor indefinitely — the
	// Slowloris shape, and cheap to run out of descriptors with. WriteTimeout
	// stays zero on purpose: watch streams are long-lived by design and a write
	// deadline would cut them.
	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// A listener error used to be swallowed by an empty if-body, so a port
	// collision left a process that was alive, silent, and serving nothing.
	listenErr := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			listenErr <- err
		}
		close(listenErr)
	}()

	// Give the listener a moment to fail loudly rather than reporting success on
	// a server that never bound its port.
	select {
	case err, open := <-listenErr:
		if open && err != nil {
			_ = ds.Close()
			return nil, fmt.Errorf("httpserve: listen on %s: %w", cfg.HTTP.Addr, err)
		}
	case <-time.After(50 * time.Millisecond):
	}

	return &ServeHandle{
		Server:    srv,
		ListenErr: listenErr,
		Close: func(ctx context.Context) error {
			// Shut the listener down first, then hand back the KVRocks
			// connections this call opened. Closing only the HTTP server left
			// the whole pool behind, which the "shuts it down gracefully"
			// contract does not allow — and it leaks on every restart of a
			// long-lived process that uses this handle.
			err := srv.Shutdown(ctx)
			if cerr := ds.Close(); err == nil {
				err = cerr
			}
			return err
		},
	}, nil
}

type serveOptions struct{ perms *Permissions }

// ServeOption configures Serve.
type ServeOption func(*serveOptions)

// WithPermissions supplies the permission set Serve should use (otherwise an
// empty, default-deny set is created — grant entries on it, or load from JSON).
func WithPermissions(p *Permissions) ServeOption {
	return func(o *serveOptions) {
		if p != nil {
			o.perms = p
		}
	}
}

// withCORS is a minimal CORS middleware honouring an explicit origin allowlist
// (use "*" to allow any). It answers preflight OPTIONS directly.
func withCORS(next http.Handler, origins []string) http.Handler {
	allow := make(map[string]bool, len(origins))
	for _, o := range origins {
		allow[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allow["*"] || allow[origin]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
