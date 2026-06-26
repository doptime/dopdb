// Package api provides typed application endpoints with a fixed hook pipeline.
//
// An endpoint is defined once with api.Api(func(in *Foo) (Bar, error)). It can
// then be invoked two ways:
//   - locally, as an ordinary function: out, err := ep.Func(&Foo{...})
//   - over HTTP, by name, through the dopdb httpserve handler.
//
// (doptime additionally forwarded calls over a Redis stream "switch"; dopdb
// drops that transport — endpoints are local + HTTP only.)
//
// The execution pipeline is intentionally minimal:
//
//	decode input -> Validate -> Func
//
// (doptime's longer hook chain — ParamEnhancer/ResultSaver/ResponseModifier — is
// dropped.) Input is decoded from the merged HTTP param map (query + body + @-context), so
// fields tagged json:"@uid" etc. are filled from the verified JWT, never the
// client — the same non-forgeable @-binding the data layer uses.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/doptime/dopdb"
)

// ErrNotFound is returned by CallByName when no endpoint matches the name.
var ErrNotFound = errors.New("api: endpoint not found")

// Ctx is a typed endpoint. The three hooks are optional and set by assigning the
// fields on the value returned by Api.
type Ctx[i any, o any] struct {
	Name string
	Func func(in i) (o, error)

	// Validate runs before Func (default: the framework validator).
	Validate func(v any) error

	iIsPtr bool
	iElem  reflect.Type
}

// Option configures an endpoint at construction.
type Option func(*config)

type config struct{ name string }

// WithName overrides the auto-derived endpoint name.
func WithName(name string) Option { return func(c *config) { c.name = cleanName(name) } }

// Api registers a typed endpoint. The name is derived from the input type unless
// WithName is given. The HTTP route is /api/<name>. Defining two endpoints with
// the same name (case-insensitively) panics — a programming error.
func Api[i any, o any](f func(in i) (o, error), opts ...Option) *Ctx[i, o] {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	iType := reflect.TypeOf((*i)(nil)).Elem()

	name := cfg.name
	if name == "" {
		name = apiNameByType(iType)
	}
	if name == "" {
		panic("api: cannot derive endpoint name from a scalar input type — pass api.WithName(\"...\")")
	}

	c := &Ctx[i, o]{Name: name, Func: f, Validate: dopdb.RunValidate}
	t := iType
	for t.Kind() == reflect.Ptr {
		c.iIsPtr = true
		t = t.Elem()
	}
	c.iElem = t

	register(c)
	return c
}

// APIName implements Handler.
func (c *Ctx[i, o]) APIName() string { return c.Name }

// CallByMap runs the full pipeline from a decoded param map plus the raw body.
// It is the single entry used by both the HTTP dispatcher and tests.
func (c *Ctx[i, o]) CallByMap(_ context.Context, params map[string]any, body []byte) (any, error) {
	in, err := c.decodeInput(params, body)
	if err != nil {
		return nil, err
	}
	if c.Validate != nil {
		if err = c.Validate(c.validationTarget(in)); err != nil {
			return nil, err
		}
	}
	return c.Func(in)
}

// decodeInput builds the typed input from the merged param map via a JSON
// round-trip (honouring json tags, including @-context tags). For a non-object
// body (a bare JSON array/scalar) it falls back to decoding the body directly.
func (c *Ctx[i, o]) decodeInput(params map[string]any, body []byte) (i, error) {
	var in i
	src, err := json.Marshal(params)
	if err != nil {
		return in, err
	}
	if len(params) == 0 && len(body) > 0 {
		src = body // non-object body case
	}
	if c.iIsPtr {
		pv := reflect.New(c.iElem)
		if err := json.Unmarshal(src, pv.Interface()); err != nil {
			return in, err
		}
		return pv.Interface().(i), nil
	}
	err = json.Unmarshal(src, &in)
	return in, err
}

// validationTarget returns a *T pointer to the input for the validator, whether
// or not i is itself a pointer type.
func (c *Ctx[i, o]) validationTarget(in i) any {
	if c.iIsPtr {
		return in
	}
	vp := reflect.New(c.iElem)
	vp.Elem().Set(reflect.ValueOf(in))
	return vp.Interface()
}

// ----------------------------------------------------------------------------
// registry
// ----------------------------------------------------------------------------

// Handler is the non-generic view of an endpoint used by the HTTP dispatcher.
type Handler interface {
	APIName() string
	CallByMap(ctx context.Context, params map[string]any, body []byte) (any, error)
}

var (
	registry   = map[string]Handler{}
	registryMu sync.RWMutex
)

func register(h Handler) {
	registryMu.Lock()
	defer registryMu.Unlock()
	key := normalizeName(h.APIName())
	if _, exists := registry[key]; exists {
		panic(fmt.Sprintf("api: endpoint %q already defined", h.APIName()))
	}
	registry[key] = h
}

// Lookup resolves an endpoint by (normalized) name.
func Lookup(name string) (Handler, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	h, ok := registry[normalizeName(name)]
	return h, ok
}

// clearRegistry resets the endpoint registry (test support).
func clearRegistry() {
	registryMu.Lock()
	registry = map[string]Handler{}
	registryMu.Unlock()
}

// Names returns the registered endpoint names (registry keys), unsorted.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// CallByName resolves and runs an endpoint. Used by the HTTP layer.
func CallByName(ctx context.Context, name string, params map[string]any, body []byte) (any, error) {
	h, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return h.CallByMap(ctx, params, body)
}

// ----------------------------------------------------------------------------
// name derivation (ported from doptime utils.ApiName / ApiNameByType)
// ----------------------------------------------------------------------------

var disallowedNames = map[string]bool{
	"": true, "string": true, "int": true, "int8": true, "int16": true,
	"int32": true, "int64": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "float32": true, "float64": true,
	"float": true, "bool": true, "byte": true, "rune": true,
	"complex64": true, "complex128": true, "map": true,
}

// affixes stripped from a TYPE name before lower-casing (prefix and suffix).
// Explicit names (WithName / RegisterDynamic) are taken literally — only cased.
var affixes = []string{"input", "param", "output", "arg", "req", "src", "data", "in", "out"}

// cleanName is the literal normalization for explicit names and lookups:
// trim + lower-case. No affix stripping, no prefix. Routing is therefore
// case-insensitive (/api/Demo and /api/demo hit the same endpoint).
func cleanName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// apiNameByType derives an endpoint name from a Go input type, stripping one
// leading/trailing affix (so DreamAnalyzerInput -> "dreamanalyzer"). Returns ""
// for a scalar/disallowed type name (the caller rejects that).
func apiNameByType(t reflect.Type) string {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	name := t.Name()
	low := strings.ToLower(name)
	for _, a := range affixes { // strip one suffix
		if strings.HasSuffix(low, a) && len(name) > len(a) {
			name = name[:len(name)-len(a)]
			break
		}
	}
	low = strings.ToLower(name)
	for _, a := range affixes { // strip one prefix
		if strings.HasPrefix(low, a) && len(name) > len(a) {
			name = name[len(a):]
			break
		}
	}
	name = cleanName(name)
	if disallowedNames[name] {
		return ""
	}
	return name
}

// normalizeName is the registry/lookup key form: lower-case (case-insensitive
// routing). A client may call /api/Demo or /api/demo interchangeably.
func normalizeName(s string) string { return cleanName(s) }

// Unregister removes an endpoint by name. No-op if absent.
func Unregister(name string) {
	registryMu.Lock()
	delete(registry, normalizeName(name))
	registryMu.Unlock()
}
