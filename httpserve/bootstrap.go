package httpserve

import (
	"context"
	"net/http"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/config"
)

// Serve is the one-line bootstrap. Given a loaded config it connects every
// [[mongo]] datasource, installs them on dopdb, builds the HTTP handler, applies
// CORS, and listens. Register your collections (dopdb.RegisterHttp) and grant
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

	sources := make([]dopdb.DatasourceConfig, 0, len(cfg.Mongo))
	for _, m := range cfg.Mongo {
		sources = append(sources, dopdb.DatasourceConfig{Name: m.Name, URI: m.URI, DB: m.DB})
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

	sources := make([]dopdb.DatasourceConfig, 0, len(cfg.Mongo))
	for _, m := range cfg.Mongo {
		sources = append(sources, dopdb.DatasourceConfig{Name: m.Name, URI: m.URI, DB: m.DB})
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

	srv := &http.Server{Addr: cfg.HTTP.Addr, Handler: handler}

	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			// listener error (e.g. port in use) — log but don't panic
		}
	}()

	return &ServeHandle{
		Server: srv,
		Close: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
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
