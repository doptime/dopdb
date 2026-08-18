package dopdb

import (
	"reflect"

	"github.com/fxamacker/cbor/v2"
)

// ----------------------------------------------------------------------------
// Storage codec: CBOR (RFC 8949).
//
// The Mongo build stored BSON. KVRocks stores opaque bytes, so dopdb owns the
// value format outright and uses CBOR: compact, self-describing, and — in
// canonical mode — deterministic, which matters here because Set members are
// deduplicated by their encoded bytes and unique-index claims are keyed by an
// encoded value.
//
// Field names come from the `json:"..."` struct tags (cbor/v2 falls back to the
// json tag when no `cbor` tag is present), so the on-disk field names are exactly
// the ones the HTTP JSON round-trip uses. That is why the former `bson:"..."`
// tags are simply gone rather than renamed.
// ----------------------------------------------------------------------------

var (
	// cborEnc is deterministic (canonical): sorted map keys, shortest-form
	// integers, definite lengths. Times are written as tagged RFC3339 strings so
	// they survive a round-trip into either time.Time or `any`.
	cborEnc cbor.EncMode
	// cborDec decodes untyped maps into map[string]any (the default is
	// map[any]any, which the query engine cannot walk).
	cborDec cbor.DecMode
)

func init() {
	eo := cbor.CanonicalEncOptions()
	eo.Time = cbor.TimeRFC3339Nano
	eo.TimeTag = cbor.EncTagRequired
	enc, err := eo.EncMode()
	if err != nil {
		panic("dopdb: cbor encoder: " + err.Error())
	}
	cborEnc = enc

	do := cbor.DecOptions{DefaultMapType: reflect.TypeOf(map[string]any(nil))}
	dec, err := do.DecMode()
	if err != nil {
		panic("dopdb: cbor decoder: " + err.Error())
	}
	cborDec = dec
}

// encodeCBOR marshals any value to the storage format.
func encodeCBOR(v any) ([]byte, error) { return cborEnc.Marshal(v) }

// decodeCBOR unmarshals storage bytes into out (a pointer).
func decodeCBOR(b []byte, out any) error { return cborDec.Unmarshal(b, out) }

// decodeDoc decodes a stored document into a generic map so the query engine and
// the unique-index bookkeeping can inspect its fields without knowing V.
func decodeDoc(b []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(b) == 0 {
		return m, nil
	}
	if err := cborDec.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
