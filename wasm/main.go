//go:build js && wasm

// Command dopdb-wasm compiles the dopdb api core to WebAssembly and exposes it
// to a JavaScript host (browser, Node, Deno, Bun, edge workers).
//
// It installs a global `dopdb` object:
//
//	dopdb.createApi(name, handlerFn)  // register a JS/TS function as /api/<name>
//	dopdb.callApi(name, input)        // -> Promise<output>; runs the endpoint
//	dopdb.removeApi(name)             // unregister
//	dopdb.apiNames()                  // -> string[]
//	dopdb.sanitizeFilter(filter)      // -> sanitized filter, or throws
//	dopdb.version                     // string
//
// handlerFn is `(input) => output | Promise<output>`. The name defaults to the
// function's name on the TS/JS side (the "type name") or an explicit string.
// Both synchronous handlers and Promise-returning handlers are supported.
package main

import (
	"context"
	"errors"
	"fmt"
	"syscall/js"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/api"
)

func main() {
	obj := js.Global().Get("Object").New()
	obj.Set("createApi", js.FuncOf(createApi))
	obj.Set("callApi", js.FuncOf(callApi))
	obj.Set("removeApi", js.FuncOf(removeApi))
	obj.Set("apiNames", js.FuncOf(apiNames))
	obj.Set("sanitizeFilter", js.FuncOf(sanitizeFilter))
	obj.Set("version", "dopdb-wasm/0.1")
	js.Global().Set("dopdb", obj)

	// Signal readiness to the host (optional hook).
	if ready := js.Global().Get("__dopdbReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {} // keep the Go runtime alive so exported funcs stay callable
}

// createApi(name, handlerFn) registers a JS handler as an endpoint at /api/<name>.
func createApi(_ js.Value, args []js.Value) any {
	if len(args) < 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeFunction {
		return jsError2("createApi(name: string, handler: function) required")
	}
	name := args[0].String()
	fn := args[1]
	api.Unregister(name) // allow re-registration (hot reload)
	registered := api.RegisterDynamic(name, func(_ context.Context, params map[string]any, _ []byte) (any, error) {
		res := fn.Invoke(goToJS(params))
		if isThenable(res) {
			settled, err := awaitPromise(res)
			if err != nil {
				return nil, err
			}
			res = settled
		}
		return jsToGo(res), nil
	})
	return registered
}

func removeApi(_ js.Value, args []js.Value) any {
	if len(args) >= 1 && args[0].Type() == js.TypeString {
		api.Unregister(args[0].String())
	}
	return js.Undefined()
}

func apiNames(_ js.Value, _ []js.Value) any {
	names := api.Names()
	arr := js.Global().Get("Array").New(len(names))
	for i, n := range names {
		arr.SetIndex(i, n)
	}
	return arr
}

// callApi(name, input) -> Promise<output>. Runs the endpoint pipeline. Returns a
// Promise because a handler may be async.
func callApi(_ js.Value, args []js.Value) any {
	if len(args) < 1 || args[0].Type() != js.TypeString {
		return jsError2("callApi(name: string, input?) required")
	}
	name := args[0].String()
	var input js.Value
	if len(args) >= 2 {
		input = args[1]
	} else {
		input = js.Undefined()
	}

	executor := js.FuncOf(func(_ js.Value, pa []js.Value) any {
		resolve, reject := pa[0], pa[1]
		go func() {
			params, _ := jsToGo(input).(map[string]any)
			if params == nil {
				params = map[string]any{}
			}
			out, err := api.CallByName(context.Background(), name, params, nil)
			if err != nil {
				reject.Invoke(jsErr(err))
				return
			}
			resolve.Invoke(goToJS(out))
		}()
		return js.Undefined()
	})
	return js.Global().Get("Promise").New(executor)
}

// sanitizeFilter(filter) -> sanitized filter object, or throws on a forbidden op.
func sanitizeFilter(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return jsError2("sanitizeFilter(filter) required")
	}
	m, _ := jsToGo(args[0]).(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	clean, err := dopdb.SanitizeFilter(dopdb.M(m))
	if err != nil {
		return jsError2(err.Error())
	}
	return goToJS(map[string]any(clean))
}

// ---- helpers ----

func isThenable(v js.Value) bool {
	return v.Type() == js.TypeObject && v.Get("then").Type() == js.TypeFunction
}

// awaitPromise blocks the current goroutine until the JS promise settles. Safe
// because callApi runs the dispatch on its own goroutine; the JS event loop
// keeps spinning and the Go wasm scheduler yields to it.
func awaitPromise(p js.Value) (js.Value, error) {
	type res struct {
		v   js.Value
		err error
	}
	ch := make(chan res, 1)
	var then, catch js.Func
	then = js.FuncOf(func(_ js.Value, a []js.Value) any {
		v := js.Undefined()
		if len(a) > 0 {
			v = a[0]
		}
		ch <- res{v: v}
		return js.Undefined()
	})
	catch = js.FuncOf(func(_ js.Value, a []js.Value) any {
		msg := "promise rejected"
		if len(a) > 0 {
			msg = jsErrString(a[0])
		}
		ch <- res{err: errors.New(msg)}
		return js.Undefined()
	})
	defer then.Release()
	defer catch.Release()
	p.Call("then", then).Call("catch", catch)
	r := <-ch
	return r.v, r.err
}

// goToJS converts a Go value (the kinds produced by jsToGo and by the param map)
// to a JS value. js.ValueOf handles map[string]any and []any recursively.
func goToJS(v any) js.Value {
	switch v.(type) {
	case nil:
		return js.Null()
	default:
		return js.ValueOf(normalizeForJS(v))
	}
}

// normalizeForJS coerces Go numeric/json types into kinds js.ValueOf accepts.
func normalizeForJS(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, vv := range t {
			m[k] = normalizeForJS(vv)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, vv := range t {
			s[i] = normalizeForJS(vv)
		}
		return s
	case fmt.Stringer:
		return t.String() // e.g. json.Number
	default:
		return v
	}
}

// jsToGo converts a JS value to a Go value (map[string]any / []any / float64 /
// string / bool / nil).
func jsToGo(v js.Value) any {
	switch v.Type() {
	case js.TypeUndefined, js.TypeNull:
		return nil
	case js.TypeBoolean:
		return v.Bool()
	case js.TypeNumber:
		return v.Float()
	case js.TypeString:
		return v.String()
	case js.TypeObject:
		if js.Global().Get("Array").Call("isArray", v).Bool() {
			n := v.Length()
			out := make([]any, n)
			for i := 0; i < n; i++ {
				out[i] = jsToGo(v.Index(i))
			}
			return out
		}
		keys := js.Global().Get("Object").Call("keys", v)
		n := keys.Length()
		out := make(map[string]any, n)
		for i := 0; i < n; i++ {
			k := keys.Index(i).String()
			out[k] = jsToGo(v.Get(k))
		}
		return out
	default:
		return nil
	}
}

func jsErr(err error) js.Value {
	return js.Global().Get("Error").New(err.Error())
}

func jsErrString(v js.Value) string {
	if v.Type() == js.TypeObject {
		if m := v.Get("message"); m.Type() == js.TypeString {
			return m.String()
		}
	}
	return v.String()
}

// jsError builds a JS Error object. Returning it (rather than panicking, which
// crashes the whole Go/wasm instance) lets the TS wrapper decide to throw.
func jsError2(msg string) js.Value {
	return js.Global().Get("Error").New(msg)
}
