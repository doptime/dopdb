package dopdb

import (
	"strings"
	"sync"
)

// Perm is a bitmask of the data commands a collection exposes over HTTP.
// It mirrors doptime/redisdb's HttpOn(<flags>) model: a collection declares
// WHICH commands the client may call, in one place, at definition time —
// registration and authorization together, no per-command Grant bookkeeping.
type Perm uint32

// One bit per command in the closed vocabulary (see httpserve.dataCommands).
const (
	HGet Perm = 1 << iota
	HSet
	HSetNX
	HDel
	Del
	HExists
	HGetAll
	HKeys
	HVals
	HLen
	HIncrBy
	HIncrByFloat
	HMSet
	HMGet
	Count
	Find
	FindOne
	Watch
	HScan
	HScanNoValues
	HRandField
)

// Convenience groups.
const (
	// ReadOnly = every non-mutating command.
	ReadOnly Perm = HGet | HExists | HGetAll | HKeys | HVals | HLen | HMGet | Count | Find | FindOne | Watch | HScan | HScanNoValues | HRandField
	// Writes = every mutating command.
	Writes Perm = HSet | HSetNX | HDel | Del | HIncrBy | HIncrByFloat | HMSet
	// All = everything. This is the HttpOn() debug default.
	All Perm = ReadOnly | Writes
	// HashAll is a doptime-compatible alias for All.
	HashAll = All
)

// cmdPerm maps an HTTP command string (upper-case) to its Perm bit.
var cmdPerm = map[string]Perm{
	"HGET": HGet, "HSET": HSet, "HSETNX": HSetNX, "HDEL": HDel, "DEL": Del,
	"HEXISTS": HExists, "HGETALL": HGetAll, "HKEYS": HKeys, "HVALS": HVals,
	"HLEN": HLen, "HINCRBY": HIncrBy, "HINCRBYFLOAT": HIncrByFloat,
	"HMSET": HMSet, "HMGET": HMGet, "COUNT": Count, "FIND": Find,
	"FINDONE": FindOne, "WATCH": Watch,
	"HSCAN": HScan, "HSCANNOVALUES": HScanNoValues, "HRANDFIELD": HRandField,
}

var (
	httpPerms   = map[string]Perm{}
	httpPermsMu sync.RWMutex
)

func setHTTPPerm(coll string, p Perm) {
	httpPermsMu.Lock()
	httpPerms[coll] = p
	httpPermsMu.Unlock()
}

// HTTPPerm returns the Perm bitmask declared for a collection (and whether it
// was registered via HttpOn at all).
func HTTPPerm(coll string) (Perm, bool) {
	httpPermsMu.RLock()
	defer httpPermsMu.RUnlock()
	p, ok := httpPerms[coll]
	return p, ok
}

// HttpAllowed reports whether command cmd is permitted on collection coll, per
// the bitmask declared by HttpOn. A collection never registered via HttpOn, or
// a command outside the closed vocabulary, is denied. The HTTP gate calls this.
func HttpAllowed(cmd, coll string) bool {
	p, ok := HTTPPerm(coll)
	if !ok {
		return false
	}
	bit, ok := cmdPerm[strings.ToUpper(cmd)]
	if !ok {
		return false
	}
	return p&bit != 0
}

// SetHttpPerm lets an agent (or runtime) overwrite a collection's exposed
// command set AFTER registration — e.g. tighten the HttpOn() debug default to
//
//	dopdb.SetHttpPerm("notes", dopdb.HGet|dopdb.HGetAll|dopdb.HSet|dopdb.HDel)
//
// Passing no perms denies everything for that collection.
func SetHttpPerm(coll string, perms ...Perm) {
	var p Perm
	for _, x := range perms {
		p |= x
	}
	setHTTPPerm(coll, p)
}

// HttpPermNames renders a Perm bitmask as the sorted command names it grants —
// handy for an audit agent introspecting what a collection currently exposes.
func HttpPermNames(p Perm) []string {
	order := []struct {
		bit  Perm
		name string
	}{
		{HGet, "hget"}, {HSet, "hset"}, {HSetNX, "hsetnx"}, {HDel, "hdel"}, {Del, "del"},
		{HExists, "hexists"}, {HGetAll, "hgetall"}, {HKeys, "hkeys"}, {HVals, "hvals"},
		{HLen, "hlen"}, {HIncrBy, "hincrby"}, {HIncrByFloat, "hincrbyfloat"},
		{HMSet, "hmset"}, {HMGet, "hmget"}, {Count, "count"}, {Find, "find"},
		{FindOne, "findone"}, {Watch, "watch"},
		{HScan, "hscan"}, {HScanNoValues, "hscannovalues"}, {HRandField, "hrandfield"},
	}
	var out []string
	for _, o := range order {
		if p&o.bit != 0 {
			out = append(out, o.name)
		}
	}
	return out
}
