package dopdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ----------------------------------------------------------------------------
// KVRocks-direct backend + multi-datasource registry.
//
// The Store/Codec abstraction is gone: dopdb is bound directly to KVRocks
// (Apache Kvrocks — a RocksDB-backed store speaking the Redis protocol). Values
// cross this boundary as CBOR bytes the generic Collection produced.
//
// LAYOUT. KVRocks has no databases-with-collections, so a datasource's logical
// database becomes a key namespace and each collection becomes one Redis key:
//
//	<ns>:<coll>                  HASH   the Hash collection: field = document id,
//	                                    value = the CBOR document
//	<ns>:<coll>:<key>            STRING/LIST/SET/ZSET for the String/List/Set/ZSet
//	                                    collection types — native Redis types, so
//	                                    every L*/S*/Z* command is the real command
//	<ns>:<coll>:__owner          HASH   key -> owner, the row-isolation index for
//	                                    the non-Hash types (a native list has no
//	                                    document to carry an owner field)
//	<ns>:<coll>:__uniq:<field>   HASH   unique-index claims (index.go)
//	<ns>:<coll>:__events         channel for WATCH (see watch)
//
// This is the redisdb layout dopdb originally came from, which is why the Hash
// family maps onto HGET/HSET/HDEL/HEXISTS/HLEN/HKEYS/HVALS/HSCAN/HRANDFIELD
// one-for-one. What KVRocks cannot do is query by content: FIND/COUNT/FINDONE
// scan and filter in-process (query.go).
//
// Multiple datasources are supported at runtime: the config file may declare
// several [[kvrocks]] sources; a request selects one with ?ds=<name>, defaulting
// to the source named "default".
// ----------------------------------------------------------------------------

// kvBackend is one KVRocks namespace dopdb talks to directly.
type kvBackend struct {
	rdb *redis.Client
	ns  string // key namespace prefix ("" = keys are unprefixed)
}

// isRedisNil reports the "no such key/field" condition.
func isRedisNil(err error) bool { return errors.Is(err, redis.Nil) }

// scanPage is how many fields one HSCAN round trip asks for while walking a
// whole collection (FIND/COUNT/HGETALL-with-scope). It bounds memory per round
// trip without making a large collection chatty.
const scanPage = int64(512)

func (b *kvBackend) prefix() string {
	if b.ns == "" {
		return ""
	}
	return b.ns + ":"
}

// hashKey is the Redis key holding a Hash collection.
func (b *kvBackend) hashKey(coll string) string { return b.prefix() + coll }

// memberKey is the Redis key holding one String/List/Set/ZSet entry.
func (b *kvBackend) memberKey(coll, key string) string { return b.prefix() + coll + ":" + key }

// entryKey is memberKey with the reserved-name check that every String/List/Set/
// ZSet path must pass.
//
// User entries and dopdb's own bookkeeping (the owner index, the change channel,
// the unique-index claim hashes) share one keyspace, so an entry literally named
// "__owner" resolves to the same Redis key as the collection's isolation index.
// Enumeration already skipped those names; the WRITE paths did not, and that is
// the dangerous direction: on KVRocks a SET over an existing hash key does not
// raise WRONGTYPE the way stock Redis does — it converts the key to a string.
// One such write therefore destroys the whole collection's owner index and every
// later scoped write fails. The names are refused instead.
func (b *kvBackend) entryKey(coll, key string) (string, error) {
	rk := b.memberKey(coll, key)
	if key == "" || isReservedKey(rk) {
		return "", fmt.Errorf("%w: %q", ErrReservedKey, key)
	}
	return rk, nil
}

// memberPattern is the glob matching every entry of a non-Hash collection.
func (b *kvBackend) memberPattern(coll, glob string) string {
	if glob == "" {
		glob = "*"
	}
	return b.prefix() + coll + ":" + glob
}

// ownerKey is the row-isolation index for the non-Hash collection types.
func (b *kvBackend) ownerKey(coll string) string { return b.prefix() + coll + ":__owner" }

// uniqKey is the claim hash backing one unique index.
func (b *kvBackend) uniqKey(coll, field string) string {
	return b.prefix() + coll + ":__uniq:" + field
}

// eventChannel is the pub/sub channel WATCH subscribes to.
func (b *kvBackend) eventChannel(coll string) string { return b.prefix() + coll + ":__events" }

// reservedSuffixes are the bookkeeping keys that must never be mistaken for a
// user entry when a non-Hash collection is enumerated.
var reservedSuffixes = []string{":__owner", ":__events"}

func isReservedKey(k string) bool {
	for _, s := range reservedSuffixes {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return strings.Contains(k, ":__uniq:")
}

// ----------------------------------------------------------------------------
// Datasource registry
// ----------------------------------------------------------------------------

// Datasources is the runtime registry of named KVRocks namespaces. The name
// "default" is required; ?ds=<name> (or WithDB) selects another. Safe for
// concurrent use.
type Datasources struct {
	mu  sync.RWMutex
	m   map[string]*kvBackend
	def string
}

// NewDatasources returns an empty registry whose default source name is "default".
func NewDatasources() *Datasources {
	return &Datasources{m: map[string]*kvBackend{}, def: "default"}
}

// Add registers a client + key namespace under name. Call Add("default", ...)
// for the default source.
func (d *Datasources) Add(name string, rdb *redis.Client, namespace string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.m[name] = &kvBackend{rdb: rdb, ns: namespace}
}

// get resolves a backend by name; "" or an unknown name falls back to default.
func (d *Datasources) get(name string) (*kvBackend, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if name == "" {
		name = d.def
	}
	b, ok := d.m[name]
	if !ok {
		b, ok = d.m[d.def]
	}
	return b, ok
}

// Names returns the registered datasource names (unsorted).
func (d *Datasources) Names() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.m))
	for k := range d.m {
		out = append(out, k)
	}
	return out
}

// Close disconnects every registered client.
func (d *Datasources) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var first error
	seen := map[*redis.Client]bool{}
	for _, b := range d.m {
		if seen[b.rdb] {
			continue
		}
		seen[b.rdb] = true
		if err := b.rdb.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// DatasourceConfig is the minimal (name, uri, namespace) a datasource needs. The
// config package resolves uri and password from the environment; this is the
// already-resolved form.
type DatasourceConfig struct {
	Name string
	// URI is a redis:// (or rediss://) URL, e.g. redis://localhost:6666.
	URI string
	// Namespace prefixes every key this datasource writes. It is the KVRocks
	// stand-in for a Mongo database name.
	Namespace string
	// Password authenticates the connection. On KVRocks the password also
	// selects the server-side namespace when namespace tokens are configured.
	Password string
}

// ConnectDatasources opens a client per source and returns a registry. Typical
// startup wires this from config:
//
//	cfg, _ := config.Load("config.toml")
//	ds, _ := dopdb.ConnectDatasources(ctx, sources)
//	dopdb.SetDatasources(ds)
func ConnectDatasources(ctx context.Context, sources []DatasourceConfig) (*Datasources, error) {
	ds := NewDatasources()
	for _, s := range sources {
		opt, err := redis.ParseURL(s.URI)
		if err != nil {
			return nil, err
		}
		if s.Password != "" {
			opt.Password = s.Password
		}
		applyPoolDefaults(opt)
		client := redis.NewClient(opt)
		if err := client.Ping(ctx).Err(); err != nil {
			_ = client.Close()
			return nil, err
		}
		ds.Add(s.Name, client, s.Namespace)
	}
	return ds, nil
}

// minPoolSize is the connection-pool floor dopdb applies when the URL does not
// set one. go-redis defaults to 10 per CPU, which starves on a small container:
// the scoped write and the in-document increment each hold a connection across
// a WATCH round trip, so a handful of concurrent writers can exhaust the pool
// and surface as "connection pool timeout" rather than as contention.
const minPoolSize = 64

// applyPoolDefaults raises an unset or too-small pool to minPoolSize. An explicit
// ?pool_size= in the URL always wins.
func applyPoolDefaults(opt *redis.Options) {
	if opt.PoolSize < minPoolSize {
		opt.PoolSize = minPoolSize
	}
	if opt.PoolTimeout == 0 {
		opt.PoolTimeout = 10 * time.Second
	}
}

// process-wide registry, installed by Serve / SetDatasources.
var defaultDatasources *Datasources

// SetDatasources installs the process-wide datasource registry.
func SetDatasources(d *Datasources) { defaultDatasources = d }

// backendFor resolves the backend for a datasource name (""=default), panicking
// if no registry is configured — the deliberate "fail loud at startup" contract.
func backendFor(ds string) *kvBackend {
	if defaultDatasources == nil {
		panic("dopdb: no datasources configured; call dopdb.SetDatasources(...) or dopdb.Serve(...) first")
	}
	b, ok := defaultDatasources.get(ds)
	if !ok {
		panic("dopdb: datasource not found and no default registered: " + ds)
	}
	return b
}

// ---- change events ----------------------------------------------------------
//
// Mongo change streams are replaced by an explicit publication: every mutating
// Hash operation publishes to <ns>:<coll>:__events. This works on any
// Redis-protocol server without requiring notify-keyspace-events to be enabled,
// and it delivers the decoded document rather than a resume-token cursor.
//
// The trade-off is stated plainly: only writes made THROUGH dopdb are seen. A
// process writing the same keys with redis-cli is invisible to watchers.

type changeEvent struct {
	Op  string          `json:"op"`
	ID  string          `json:"id"`
	Doc json.RawMessage `json:"doc,omitempty"`
}

// publish emits one change event. Failures are ignored: a watcher missing an
// event must never fail the write that produced it.
func (b *kvBackend) publish(ctx context.Context, coll, op, id string, doc []byte) {
	ev := changeEvent{Op: op, ID: id}
	if len(doc) > 0 {
		if m, err := decodeDoc(doc); err == nil {
			if j, err := json.Marshal(m); err == nil {
				ev.Doc = j
			}
		}
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = b.rdb.Publish(ctx, b.eventChannel(coll), payload).Err()
}

// ---- index ------------------------------------------------------------------

// ensureIndex records an index declaration. On KVRocks only Unique has runtime
// meaning (see index.go); the others are accepted and inert so struct tags keep
// their vocabulary. It never errors — there is nothing to create on the server.
func (b *kvBackend) ensureIndex(ctx context.Context, coll string, idx IndexSpec) error {
	_ = ctx
	if !idx.Unique {
		return nil
	}
	fields := make([]string, 0, len(idx.Keys))
	for _, k := range idx.Keys {
		fields = append(fields, k.Field)
	}
	if len(fields) == 0 {
		return nil
	}
	existing := uniqueFieldsOf(coll)
	for _, f := range fields {
		found := false
		for _, e := range existing {
			if e == f {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, f)
		}
	}
	registerUniqueFields(coll, existing)
	return nil
}

// ---- optimistic transactions -------------------------------------------------
//
// WATCH is Redis's compare-and-set primitive and dopdb uses it for the two
// operations that must read-then-write a document: the scoped write and the
// in-document increment. Its granularity is the KEY, and a Hash collection is
// ONE key — so a concurrent write to any document in the collection aborts the
// transaction, not just a write to the same document. That is the price of the
// one-hash-per-collection layout (which is what makes HGET/HSET/HSCAN the real
// commands), and it is paid in retries rather than in correctness: an aborted
// transaction never writes.
//
// watchRetry therefore retries generously with jittered backoff, and reports a
// contention error rather than silently dropping the write if it truly cannot
// land.

const watchAttempts = 64

func watchRetry(ctx context.Context, rdb *redis.Client, what string, keys []string, txf func(*redis.Tx) error) error {
	for i := 0; i < watchAttempts; i++ {
		err := rdb.Watch(ctx, txf, keys...)
		if err == nil {
			return nil
		}
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
		// contended: back off a little, with jitter so retries de-synchronise
		delay := time.Duration(i+1) * 200 * time.Microsecond
		if delay > 20*time.Millisecond {
			delay = 20 * time.Millisecond
		}
		jitter := time.Duration(rand.Int63n(int64(delay) + 1))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay/2 + jitter):
		}
	}
	return errors.New("dopdb: write contention on " + what)
}

// ---- writes ------------------------------------------------------------------

func (b *kvBackend) put(ctx context.Context, coll, id string, doc []byte) error {
	commit, rollback, err := b.enforceUnique(ctx, coll, id, doc)
	if err != nil {
		return err
	}
	existed, err := b.rdb.HExists(ctx, b.hashKey(coll), id).Result()
	if err != nil {
		rollback()
		return err
	}
	if err := b.rdb.HSet(ctx, b.hashKey(coll), id, doc).Err(); err != nil {
		rollback()
		return err
	}
	commit()
	op := "insert"
	if existed {
		op = "replace"
	}
	b.publish(ctx, coll, op, id, doc)
	return nil
}

// putScoped is the atomic scoped write: it succeeds only when the stored
// document is absent or already owned by ownerVal. WATCH/MULTI/EXEC closes the
// check-then-act window that a plain read-then-write would open (the Mongo build
// got the same guarantee from a filtered upsert).
func (b *kvBackend) putScoped(ctx context.Context, coll, id string, doc []byte, ownerField, ownerVal string) error {
	hk := b.hashKey(coll)
	existed := false
	txf := func(tx *redis.Tx) error {
		prev, err := tx.HGet(ctx, hk, id).Bytes()
		if err != nil && !isRedisNil(err) {
			return err
		}
		existed = err == nil
		if existed {
			m, derr := decodeDoc(prev)
			if derr != nil {
				return derr
			}
			if cur, ok := m[ownerField]; ok && !equalValues(cur, ownerVal) {
				return ErrForbidden
			}
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HSet(ctx, hk, id, doc)
			return nil
		})
		return err
	}
	commit, rollback, err := b.enforceUnique(ctx, coll, id, doc)
	if err != nil {
		return err
	}
	if err := watchRetry(ctx, b.rdb, coll+":"+id, []string{hk}, txf); err != nil {
		// forbidden, or contention exhausted: the write did NOT happen, so the
		// values this call claimed must not stay claimed
		rollback()
		return err
	}
	commit()
	op := "insert"
	if existed {
		op = "replace"
	}
	b.publish(ctx, coll, op, id, doc)
	return nil
}

func (b *kvBackend) putIfAbsent(ctx context.Context, coll, id string, doc []byte) (bool, error) {
	commit, rollback, err := b.enforceUnique(ctx, coll, id, doc)
	if err != nil {
		return false, err
	}
	inserted, err := b.rdb.HSetNX(ctx, b.hashKey(coll), id, doc).Result()
	if err != nil {
		rollback()
		return false, err
	}
	if !inserted {
		// the id was taken, so nothing was written — releasing the values this
		// call claimed is what keeps a no-op insert from blocking a later,
		// legitimate writer of the same unique value
		rollback()
		return false, nil
	}
	commit()
	b.publish(ctx, coll, "insert", id, doc)
	return true, nil
}

func (b *kvBackend) putMany(ctx context.Context, coll string, ids []string, docs [][]byte) error {
	if len(ids) == 0 {
		return nil
	}
	if len(uniqueFieldsOf(coll)) > 0 {
		// with a unique constraint each document needs its own claim/release
		for i, id := range ids {
			if err := b.put(ctx, coll, id, docs[i]); err != nil {
				return err
			}
		}
		return nil
	}
	pairs := make([]any, 0, len(ids)*2)
	for i, id := range ids {
		pairs = append(pairs, id, docs[i])
	}
	if err := b.rdb.HSet(ctx, b.hashKey(coll), pairs...).Err(); err != nil {
		return err
	}
	for i, id := range ids {
		b.publish(ctx, coll, "replace", id, docs[i])
	}
	return nil
}

// incr atomically adds delta to a numeric field (dot path) of a document,
// upserting the document if absent. Redis HINCRBYFLOAT increments a hash FIELD,
// but a field here holds a whole CBOR document, so the increment is an optimistic
// read-modify-write guarded by WATCH — still atomic, just not a single command.
// applyIncr computes the new value of a numeric field inside a decoded document.
//
// A field that already holds a string, bool, array or object is REFUSED
// (ErrFieldType) rather than replaced by a number: Mongo's $inc refused it too,
// and silently overwriting is data loss dressed up as success. An absent field —
// or one explicitly null — starts from zero, which destroys nothing.
func applyIncr(m map[string]any, fieldPath string, delta float64) error {
	cur := 0.0
	if v, ok := lookupPath(m, fieldPath); ok && v != nil {
		f, isNum := asFloat(v)
		if !isNum {
			return fmt.Errorf("%w: %s holds %s", ErrFieldType, fieldPath, typeName(v))
		}
		cur = f
	}
	next := cur + delta
	// Keep an integral result an integer. CBOR is typed, so writing 25.0 where
	// the struct field is an int would make the document undecodable on the next
	// read — the same reason Mongo's $inc kept int+int an int.
	if next == math.Trunc(next) && !math.IsInf(next, 0) {
		setPath(m, fieldPath, int64(next))
	} else {
		setPath(m, fieldPath, next)
	}
	return nil
}

func (b *kvBackend) incr(ctx context.Context, coll, id, fieldPath string, delta float64) error {
	return b.incrScoped(ctx, coll, id, fieldPath, delta, "", "")
}

// incrScoped is the increment, with the ownership test INSIDE the transaction.
//
// The check used to live in the HTTP dispatcher: "does a document with this id
// and this owner exist?", then a separate, unscoped increment. That is a
// check-then-act with two failure modes, both reachable by an owner racing their
// own delete-and-recreate:
//
//   - the document is gone by the time the increment runs, and the increment
//     UPSERTS it — creating a document with no owner field at all. Scoped reads
//     never match it and scoped deletes never reach it: a permanent ghost row.
//   - a third party recreates the id in the window, and the increment lands on
//     THEIR document.
//
// So the owner test moved in here, beside the write, under the same WATCH. When
// ownerField is empty the call is unscoped and behaves as before; when it is set,
// a missing document is ErrForbidden rather than an upsert — there is nothing to
// own yet, and inventing an unowned row is exactly the bug.
func (b *kvBackend) incrScoped(ctx context.Context, coll, id, fieldPath string, delta float64, ownerField, ownerVal string) error {
	hk := b.hashKey(coll)
	scoped := ownerField != ""
	txf := func(tx *redis.Tx) error {
		var m map[string]any
		prev, err := tx.HGet(ctx, hk, id).Bytes()
		switch {
		case isRedisNil(err):
			if scoped {
				// no document to own: refuse rather than create an unowned one
				return ErrForbidden
			}
			m = map[string]any{}
		case err != nil:
			return err
		default:
			if m, err = decodeDoc(prev); err != nil {
				return err
			}
			if scoped {
				cur, ok := m[ownerField]
				if !ok || !equalValues(cur, ownerVal) {
					return ErrForbidden
				}
			}
		}
		if err := applyIncr(m, fieldPath, delta); err != nil {
			return err
		}
		doc, err := encodeCBOR(m)
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HSet(ctx, hk, id, doc)
			return nil
		})
		if err == nil {
			b.publish(ctx, coll, "update", id, doc)
		}
		return err
	}
	return watchRetry(ctx, b.rdb, coll+":"+id, []string{hk}, txf)
}

// ---- reads -------------------------------------------------------------------

func (b *kvBackend) get(ctx context.Context, coll, id string) ([]byte, error) {
	raw, err := b.rdb.HGet(ctx, b.hashKey(coll), id).Bytes()
	if isRedisNil(err) {
		return nil, ErrNoDoc
	}
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (b *kvBackend) getMany(ctx context.Context, coll string, ids []string) ([][]byte, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	vals, err := b.rdb.HMGet(ctx, b.hashKey(coll), ids...).Result()
	if err != nil {
		return nil, err
	}
	out := make([][]byte, len(ids))
	for i := range ids {
		if i < len(vals) {
			if s, ok := vals[i].(string); ok {
				out[i] = []byte(s)
			}
		}
	}
	return out, nil
}

func (b *kvBackend) del(ctx context.Context, coll string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if len(uniqueFieldsOf(coll)) > 0 {
		if docs, err := b.getMany(ctx, coll, ids); err == nil {
			b.dropUnique(ctx, coll, ids, docs)
		}
	}
	// One HDEL per id, pipelined: same single round trip as a batched HDEL, but
	// each reply says whether THAT id existed, so the change events are exact
	// instead of announcing a deletion for every id in a mixed batch.
	pipe := b.rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HDel(ctx, b.hashKey(coll), id)
	}
	if _, err := pipe.Exec(ctx); err != nil && !isRedisNil(err) {
		return 0, err
	}
	var n int64
	for i, cmd := range cmds {
		if cmd.Val() > 0 {
			n += cmd.Val()
			b.publish(ctx, coll, "delete", ids[i], nil)
		}
	}
	return n, nil
}

func (b *kvBackend) exists(ctx context.Context, coll, id string) (bool, error) {
	return b.rdb.HExists(ctx, b.hashKey(coll), id).Result()
}

func (b *kvBackend) ids(ctx context.Context, coll string) ([]string, error) {
	out, err := b.rdb.HKeys(ctx, b.hashKey(coll)).Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func (b *kvBackend) all(ctx context.Context, coll string) ([]string, [][]byte, error) {
	m, err := b.rdb.HGetAll(ctx, b.hashKey(coll)).Result()
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids) // HGETALL order is unspecified; make it reproducible
	docs := make([][]byte, len(ids))
	for i, id := range ids {
		docs[i] = []byte(m[id])
	}
	return ids, docs, nil
}

func (b *kvBackend) count(ctx context.Context, coll string) (int64, error) {
	return b.rdb.HLen(ctx, b.hashKey(coll)).Result()
}

// walk streams the whole collection hash through visit, page by page, so a large
// collection never materialises twice. visit returning false stops the walk.
func (b *kvBackend) walk(ctx context.Context, coll string, visit func(id string, raw []byte) (bool, error)) error {
	var cursor uint64
	hk := b.hashKey(coll)
	for {
		vals, next, err := b.rdb.HScan(ctx, hk, cursor, "", scanPage).Result()
		if err != nil {
			return err
		}
		for i := 0; i+1 < len(vals); i += 2 {
			cont, err := visit(vals[i], []byte(vals[i+1]))
			if err != nil {
				return err
			}
			if !cont {
				return nil
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

// find is the FIND/FINDONE path: scan the collection, decode each document,
// evaluate the (already sanitized) filter in-process, then sort/skip/limit/project.
func (b *kvBackend) find(ctx context.Context, coll string, filter M, opt FindOpt) ([]string, [][]byte, error) {
	var rows []row
	err := b.walk(ctx, coll, func(id string, raw []byte) (bool, error) {
		doc, err := decodeDoc(raw)
		if err != nil {
			return false, err
		}
		doc["_id"] = id // the id lives in the hash field, not in the document
		if !matchFilter(doc, filter) {
			return true, nil
		}
		rows = append(rows, row{id: id, doc: doc, raw: raw})
		return true, nil
	})
	if err != nil {
		return nil, nil, err
	}
	// A stable base order keeps paging and sorting reproducible; HSCAN order is
	// unspecified and may differ between two servers holding the same data.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	rows, err = applyFindOpt(rows, opt)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(rows))
	docs := make([][]byte, len(rows))
	for i, r := range rows {
		ids[i] = r.id
		docs[i] = r.raw
	}
	return ids, docs, nil
}

// countFilter counts documents matching an (already sanitized) filter.
func (b *kvBackend) countFilter(ctx context.Context, coll string, filter M) (int64, error) {
	var n int64
	err := b.walk(ctx, coll, func(id string, raw []byte) (bool, error) {
		doc, derr := decodeDoc(raw)
		if derr != nil {
			return false, derr
		}
		doc["_id"] = id
		if matchFilter(doc, filter) {
			n++
		}
		return true, nil
	})
	return n, err
}

// watch subscribes to the collection's change channel and invokes emit for each
// event. For a scoped collection (scope non-nil) an event is delivered only when
// its document satisfies the owner predicate; delete events carry no document
// and are therefore not delivered to scoped watchers — the same visible
// behaviour the change-stream implementation had. Returns when ctx is cancelled
// or emit reports the consumer is gone.
func (b *kvBackend) watch(ctx context.Context, coll string, scope M, emit func(op, id string, raw []byte) error) error {
	sub := b.rdb.Subscribe(ctx, b.eventChannel(coll))
	defer func() { _ = sub.Close() }()
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			var ev changeEvent
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				continue
			}
			var raw []byte
			var doc map[string]any
			if len(ev.Doc) > 0 {
				if err := json.Unmarshal(ev.Doc, &doc); err == nil {
					doc["_id"] = ev.ID
					if enc, err := encodeCBOR(doc); err == nil {
						raw = enc
					}
				}
			}
			if len(scope) > 0 {
				if doc == nil || !matchFilter(doc, scope) {
					continue
				}
			}
			if err := emit(ev.Op, ev.ID, raw); err != nil {
				return err // consumer gone (e.g. client disconnected)
			}
		}
	}
}

// ---- Hash scan/sample primitives (Redis HSCAN / HRANDFIELD, natively) --------

// sample returns up to count random document ids (Redis HRANDFIELD). Unscoped it
// is the real command; with an owner scope the population has to be filtered
// first, so it falls back to a scan.
func (b *kvBackend) sample(ctx context.Context, coll string, count int, scope M) ([]string, error) {
	if count <= 0 {
		count = 1
	}
	if len(scope) == 0 {
		ids, err := b.rdb.HRandField(ctx, b.hashKey(coll), count).Result()
		if err != nil && !isRedisNil(err) {
			return nil, err
		}
		return ids, nil
	}
	ids, _, err := b.find(ctx, coll, scope, FindOpt{})
	if err != nil {
		return nil, err
	}
	if len(ids) > count {
		ids = ids[:count]
	}
	return ids, nil
}

// scan paginates a Hash collection (Redis HSCAN). match is a Redis glob applied
// to the document id by the server. The cursor is the server's own opaque
// cursor, so — exactly as with real HSCAN — a page may come back short (or
// empty) with a non-zero cursor, and only cursor 0 means the iteration is done.
// scope, when non-nil, filters the page by owner after it arrives.
func (b *kvBackend) scan(ctx context.Context, coll, match string, cursor uint64, count int64, scope M) ([]string, [][]byte, uint64, error) {
	if count <= 0 {
		count = 10
	}
	if match == "" {
		match = "*"
	}
	vals, next, err := b.rdb.HScan(ctx, b.hashKey(coll), cursor, match, count).Result()
	if err != nil {
		return nil, nil, 0, err
	}
	type kv struct {
		id  string
		raw []byte
	}
	page := make([]kv, 0, len(vals)/2)
	for i := 0; i+1 < len(vals); i += 2 {
		page = append(page, kv{id: vals[i], raw: []byte(vals[i+1])})
	}
	sort.Slice(page, func(i, j int) bool { return page[i].id < page[j].id })

	ids := make([]string, 0, len(page))
	docs := make([][]byte, 0, len(page))
	for _, e := range page {
		if len(scope) > 0 {
			doc, derr := decodeDoc(e.raw)
			if derr != nil {
				return nil, nil, 0, derr
			}
			doc["_id"] = e.id
			if !matchFilter(doc, scope) {
				continue
			}
		}
		ids = append(ids, e.id)
		docs = append(docs, e.raw)
	}
	return ids, docs, next, nil
}

// ---- owner index for the non-Hash collection types ---------------------------
//
// A native Redis list/set/zset has no document in which to store an owner, so
// row isolation for those types is kept in a side hash. checkOwner is the read
// guard; claimOwner is the write guard (first writer owns the key).

// scopeOwner extracts the single (field, value) pair of an owner scope.
func scopeOwner(scope M) (string, string, bool) {
	for k, v := range scope {
		if s, ok := v.(string); ok {
			return k, s, true
		}
		return k, toStringValue(v), true
	}
	return "", "", false
}

func toStringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.Trim(string(b), `"`)
}

// checkOwner reports whether key may be read/written under scope. An unowned key
// is readable by anyone (it does not exist yet); a key owned by someone else is
// not.
func (b *kvBackend) checkOwner(ctx context.Context, coll, key string, scope M) error {
	if len(scope) == 0 {
		return nil
	}
	_, want, ok := scopeOwner(scope)
	if !ok {
		return nil
	}
	got, err := b.rdb.HGet(ctx, b.ownerKey(coll), key).Result()
	if isRedisNil(err) {
		return nil // unclaimed
	}
	if err != nil {
		return err
	}
	if got != want {
		return ErrForbidden
	}
	return nil
}

// claimOwner takes ownership of key for the caller, or fails if it is already
// held by someone else. HSETNX makes the claim atomic.
//
// It also reclaims STALE claims. A claim outlives its data in three ordinary
// ways: Redis drops a list/set/zset key the moment its last element is removed,
// a String key can expire on its TTL, and a crash can land between the claim and
// the write. Without this, the first user to touch a key name owns it forever —
// everyone else gets a permanent 403 for a key that does not exist, and the
// owner hash grows without bound. So when the holder differs and the underlying
// key is gone, ownership transfers, under a WATCH so two racing claimants cannot
// both win.
func (b *kvBackend) claimOwner(ctx context.Context, coll, key string, scope M) error {
	if len(scope) == 0 {
		return nil
	}
	_, want, ok := scopeOwner(scope)
	if !ok {
		return nil
	}
	ok2, err := b.rdb.HSetNX(ctx, b.ownerKey(coll), key, want).Result()
	if err != nil {
		return err
	}
	if ok2 {
		return nil
	}
	got, err := b.rdb.HGet(ctx, b.ownerKey(coll), key).Result()
	if err != nil && !isRedisNil(err) {
		return err
	}
	if got == want {
		return nil
	}
	return b.takeOverStaleClaim(ctx, coll, key, got, want)
}

// takeOverStaleClaim transfers a claim whose data no longer exists.
func (b *kvBackend) takeOverStaleClaim(ctx context.Context, coll, key, holder, want string) error {
	ok := b.rdb.Exists(ctx, b.memberKey(coll, key))
	if n, err := ok.Result(); err != nil {
		return err
	} else if n > 0 {
		return ErrForbidden // the holder's data is really there
	}
	txf := func(tx *redis.Tx) error {
		// re-check under WATCH: the holder may have rewritten in the meantime
		cur, err := tx.HGet(ctx, b.ownerKey(coll), key).Result()
		if err != nil && !isRedisNil(err) {
			return err
		}
		if err == nil && cur != holder && cur != want {
			return ErrForbidden
		}
		n, err := tx.Exists(ctx, b.memberKey(coll, key)).Result()
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrForbidden
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HSet(ctx, b.ownerKey(coll), key, want)
			return nil
		})
		return err
	}
	return watchRetry(ctx, b.rdb, coll+":"+key, []string{b.ownerKey(coll), b.memberKey(coll, key)}, txf)
}

// releaseIfEmpty drops the ownership record when the entry key no longer exists.
//
// Redis deletes a list/set/zset key as soon as its last element goes, so every
// path that can empty one has to call this — otherwise the claim outlives the
// data and the owner hash grows forever. claimOwner can recover from a missed
// call, but recovering is not a reason to leak.
func (b *kvBackend) releaseIfEmpty(ctx context.Context, coll, key string, scope M) {
	if len(scope) == 0 {
		return
	}
	if n, err := b.rdb.Exists(ctx, b.memberKey(coll, key)).Result(); err == nil && n == 0 {
		b.releaseOwner(ctx, coll, key)
	}
}

// releaseOwner drops the ownership record for a deleted key.
func (b *kvBackend) releaseOwner(ctx context.Context, coll string, keys ...string) {
	if len(keys) == 0 {
		return
	}
	_ = b.rdb.HDel(ctx, b.ownerKey(coll), keys...).Err()
}

// ownedKeys lists the entry keys of a non-Hash collection, filtered by scope.
// It walks the keyspace with SCAN (never KEYS) so a large namespace stays
// responsive.
func (b *kvBackend) ownedKeys(ctx context.Context, coll, glob string, scope M) ([]string, error) {
	pattern := b.memberPattern(coll, glob)
	trim := b.prefix() + coll + ":"
	var (
		cursor uint64
		out    []string
	)
	for {
		keys, next, err := b.rdb.Scan(ctx, cursor, pattern, scanPage).Result()
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if isReservedKey(k) {
				continue
			}
			out = append(out, strings.TrimPrefix(k, trim))
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	sort.Strings(out)
	if len(scope) == 0 {
		return out, nil
	}
	_, want, ok := scopeOwner(scope)
	if !ok {
		return out, nil
	}
	owners, err := b.rdb.HGetAll(ctx, b.ownerKey(coll)).Result()
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, k := range out {
		if o, held := owners[k]; !held || o == want {
			kept = append(kept, k)
		}
	}
	return kept, nil
}
