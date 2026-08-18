package dopdb

// ----------------------------------------------------------------------------
// keyspace — the handle behind the String/List/Set/ZSet collection types.
//
// On Mongo those four were emulated: each was a Collection[K, *someDoc] whose
// documents carried an `items`/`members`/`v` array and an `owner` field, and
// every command was array surgery on that document. On KVRocks they are the
// real thing — one native Redis key per entry — so they no longer need (or want)
// the generic Collection machinery: no write plan, no index specs, no value
// codec for a wrapper document.
//
// What they DO still need is a collection name, a bound datasource, and the
// backend lookup. That is all this type is.
// ----------------------------------------------------------------------------

type keyspace struct {
	coll string
	ds   string
}

// newKeyspace builds the handle from the shared Option set. Unlike the Hash
// collection there is no value type to derive a name from, so WithCollection
// (or WithKey) is required — being explicit beats silently naming a collection
// after an internal wrapper struct.
func newKeyspace(kind string, opts ...Option) keyspace {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.collection == "" {
		panic("dopdb: " + kind + " needs a name; pass WithCollection(...)")
	}
	return keyspace{coll: cfg.collection, ds: cfg.db}
}

// backend resolves the datasource for this keyspace (""→the bound one, itself
// ""→the registry default).
func (k keyspace) backend(ds string) *kvBackend {
	if ds == "" {
		ds = k.ds
	}
	return backendFor(ds)
}

// permsFrom folds the variadic HttpOn perms into one bitmask, with the debug
// default (everything on) when none are given.
func permsFrom(perms []Perm) Perm {
	if len(perms) == 0 {
		return All
	}
	var p Perm
	for _, x := range perms {
		p |= x
	}
	return p
}
