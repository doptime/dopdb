package dopdb

import (
	"context"
	"fmt"
	"sync"
)

// ----------------------------------------------------------------------------
// Unique indexes.
//
// Mongo enforced `index:"unique"` in the server and reported E11000. KVRocks has
// no secondary indexes, so dopdb enforces the constraint itself: for each unique
// field of a collection it keeps a side hash
//
//	<ns>:<coll>:__uniq:<field>   field = encoded value, value = document id
//
// and every write claims its values there before the document lands, releasing
// the values it no longer holds afterwards.
//
// Two honest limitations, both documented in docs/01-data.md:
//   - The claim and the document write are separate commands, so a crash between
//     them can leave a stale claim. A stale claim is self-healing: the next write
//     of the same document id re-claims it, and a read is unaffected.
//   - A missing/nil value is not claimed (sparse behaviour), so several documents
//     may omit a unique field. Mongo's non-sparse unique index would have
//     rejected the second null; this is the deliberate difference.
// ----------------------------------------------------------------------------

var (
	uniqueFields   = map[string][]string{}
	uniqueFieldsMu sync.RWMutex
)

// registerUniqueFields records the unique-tagged fields of a collection. It is
// called once per collection from the lazy index setup in dopdb.go.
func registerUniqueFields(coll string, fields []string) {
	if len(fields) == 0 {
		return
	}
	uniqueFieldsMu.Lock()
	defer uniqueFieldsMu.Unlock()
	uniqueFields[coll] = fields
}

// uniqueFieldsOf returns the unique-tagged fields of a collection (nil if none).
func uniqueFieldsOf(coll string) []string {
	uniqueFieldsMu.RLock()
	defer uniqueFieldsMu.RUnlock()
	return uniqueFields[coll]
}

// uniqueValueKey renders a field value as the hash field used to claim it.
// Encoding is canonical CBOR, so 1 and 1.0 claim the same slot exactly as two
// equal values should.
func uniqueValueKey(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	b, err := encodeCBOR(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// takenSlot records one unique-value slot this call claimed, so a write that
// never lands can give it back.
type takenSlot struct {
	field string
	slot  string
	fresh bool // true when this call created the claim (nobody held it before)
}

// claimUnique verifies and takes the unique-value slots a document needs.
// It returns ErrDuplicate if any value is already held by a different id, plus
// the slots it took so the caller can undo them.
func (b *kvBackend) claimUnique(ctx context.Context, coll, id string, doc map[string]any, fields []string) ([]takenSlot, error) {
	var taken []takenSlot
	for _, f := range fields {
		v, ok := doc[f]
		if !ok {
			continue
		}
		slot, ok := uniqueValueKey(v)
		if !ok {
			continue
		}
		idxKey := b.uniqKey(coll, f)
		holder, err := b.rdb.HGet(ctx, idxKey, slot).Result()
		if err == nil && holder != id {
			return taken, fmt.Errorf("%w: %s.%s", ErrDuplicate, coll, f)
		}
		if err != nil && !isRedisNil(err) {
			return taken, err
		}
		fresh := isRedisNil(err) // nobody held this value before
		if err := b.rdb.HSet(ctx, idxKey, slot, id).Err(); err != nil {
			return taken, err
		}
		taken = append(taken, takenSlot{field: f, slot: slot, fresh: fresh})
	}
	return taken, nil
}

// unclaim gives back the slots a call took when its write did not land. Only the
// slots this call CREATED are dropped: one it found already pointing at the same
// id belongs to the stored document and must survive.
func (b *kvBackend) unclaim(ctx context.Context, coll, id string, taken []takenSlot) {
	for _, t := range taken {
		if !t.fresh {
			continue
		}
		if holder, err := b.rdb.HGet(ctx, b.uniqKey(coll, t.field), t.slot).Result(); err == nil && holder == id {
			_ = b.rdb.HDel(ctx, b.uniqKey(coll, t.field), t.slot).Err()
		}
	}
}

// releaseUnique drops the slots that oldDoc held and newDoc no longer does.
// newDoc nil means the document was deleted, so every slot is released.
func (b *kvBackend) releaseUnique(ctx context.Context, coll, id string, oldDoc, newDoc map[string]any, fields []string) {
	if oldDoc == nil {
		return
	}
	for _, f := range fields {
		oldV, ok := oldDoc[f]
		if !ok {
			continue
		}
		oldSlot, ok := uniqueValueKey(oldV)
		if !ok {
			continue
		}
		if newDoc != nil {
			if newSlot, ok := uniqueValueKey(newDoc[f]); ok && newSlot == oldSlot {
				continue // still held by the same document
			}
		}
		// only release a slot this document actually holds
		if holder, err := b.rdb.HGet(ctx, b.uniqKey(coll, f), oldSlot).Result(); err == nil && holder == id {
			_ = b.rdb.HDel(ctx, b.uniqKey(coll, f), oldSlot).Err()
		}
	}
}

// enforceUnique is the whole write-side protocol in one call: read the previous
// document, claim the new values, and hand back the two ways the call can end.
//
//   - commit()   — the document was written: release the values it no longer holds.
//   - rollback() — the write did NOT happen (error, forbidden, contention, or an
//     insert that found the id taken): give back the values this call claimed.
//
// Exactly one of them must run. Without rollback, a write that fails after the
// claim leaves the value reserved for a document that does not have it, and the
// next writer of that value gets a spurious 409 until the process restarts.
func (b *kvBackend) enforceUnique(ctx context.Context, coll, id string, newDoc []byte) (commit func(), rollback func(), err error) {
	noop := func() {}
	fields := uniqueFieldsOf(coll)
	if len(fields) == 0 {
		return noop, noop, nil
	}
	nd, err := decodeDoc(newDoc)
	if err != nil {
		return nil, nil, err
	}
	var od map[string]any
	if prev, err := b.rdb.HGet(ctx, b.hashKey(coll), id).Bytes(); err == nil {
		if m, derr := decodeDoc(prev); derr == nil {
			od = m
		}
	} else if !isRedisNil(err) {
		return nil, nil, err
	}
	taken, err := b.claimUnique(ctx, coll, id, nd, fields)
	if err != nil {
		// a rejected claim must not leave the earlier fields of the same
		// document claimed either
		b.unclaim(ctx, coll, id, taken)
		return nil, nil, err
	}
	return func() { b.releaseUnique(ctx, coll, id, od, nd, fields) },
		func() { b.unclaim(ctx, coll, id, taken) },
		nil
}

// dropUnique releases every slot held by the given ids (used by delete).
func (b *kvBackend) dropUnique(ctx context.Context, coll string, ids []string, docs [][]byte) {
	fields := uniqueFieldsOf(coll)
	if len(fields) == 0 {
		return
	}
	for i, id := range ids {
		if i >= len(docs) || docs[i] == nil {
			continue
		}
		if od, err := decodeDoc(docs[i]); err == nil {
			b.releaseUnique(ctx, coll, id, od, nil, fields)
		}
	}
}
