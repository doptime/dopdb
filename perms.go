package dopdb

import (
	"strings"
	"sync"
)

// Perm is a bitmask of the data commands a collection exposes over HTTP.
// It mirrors doptime/redisdb's HttpOn(<flags>) model: a collection declares
// WHICH commands the client may call, in one place, at definition time —
// registration and authorization together, no per-command Grant bookkeeping.
type Perm uint64

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
	StrGet
	StrSet
	StrSetAll
	StrGetAll
	StrDel
	SAdd
	SRem
	SMembers
	SIsMember
	SCard
	LPush
	RPush
	LPop
	RPop
	LRange
	LLen
	LIndex
	LSet
	LRem
	LTrim
	LInsertBefore
	LInsertAfter
	ZAdd
	ZRem
	ZScore
	ZCard
	ZCount
	ZIncrBy
	ZRange
	ZRevRange
	ZRangeByScore
	ZRevRangeByScore
	ZRank
	ZRevRank
	ZPopMin
	ZPopMax
	ZRemRangeByRank
	ZRemRangeByScore
)

// Convenience groups.
const (
	// ReadOnly = every non-mutating command.
	ReadOnly Perm = HGet | HExists | HGetAll | HKeys | HVals | HLen | HMGet | Count | Find | FindOne | Watch | HScan | HScanNoValues | HRandField | StrGet | StrGetAll | SMembers | SIsMember | SCard | LRange | LLen | LIndex | ZScore | ZCard | ZCount | ZRange | ZRevRange | ZRangeByScore | ZRevRangeByScore | ZRank | ZRevRank
	// Writes = every mutating command.
	Writes Perm = HSet | HSetNX | HDel | Del | HIncrBy | HIncrByFloat | HMSet | StrSet | StrSetAll | StrDel | SAdd | SRem | LPush | RPush | LPop | RPop | LSet | LRem | LTrim | LInsertBefore | LInsertAfter | ZAdd | ZRem | ZIncrBy | ZPopMin | ZPopMax | ZRemRangeByRank | ZRemRangeByScore
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
	"STRGET": StrGet, "STRSET": StrSet, "STRSETALL": StrSetAll, "STRGETALL": StrGetAll, "STRDEL": StrDel,
	"SADD": SAdd, "SREM": SRem, "SMEMBERS": SMembers, "SISMEMBER": SIsMember, "SCARD": SCard,
	"LPUSH": LPush, "RPUSH": RPush, "LPOP": LPop, "RPOP": RPop, "LRANGE": LRange,
	"LLEN": LLen, "LINDEX": LIndex, "LSET": LSet, "LREM": LRem, "LTRIM": LTrim,
	"LINSERTBEFORE": LInsertBefore, "LINSERTAFTER": LInsertAfter,
	"ZADD": ZAdd, "ZREM": ZRem, "ZSCORE": ZScore, "ZCARD": ZCard, "ZCOUNT": ZCount, "ZINCRBY": ZIncrBy,
	"ZRANGE": ZRange, "ZREVRANGE": ZRevRange, "ZRANGEBYSCORE": ZRangeByScore, "ZREVRANGEBYSCORE": ZRevRangeByScore,
	"ZRANK": ZRank, "ZREVRANK": ZRevRank, "ZPOPMIN": ZPopMin, "ZPOPMAX": ZPopMax,
	"ZREMRANGEBYRANK": ZRemRangeByRank, "ZREMRANGEBYSCORE": ZRemRangeByScore,
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
		{StrGet, "strget"}, {StrSet, "strset"}, {StrSetAll, "strsetall"}, {StrGetAll, "strgetall"}, {StrDel, "strdel"}, {SAdd, "sadd"}, {SRem, "srem"}, {SMembers, "smembers"}, {SIsMember, "sismember"}, {SCard, "scard"}, {LPush, "lpush"}, {RPush, "rpush"}, {LPop, "lpop"}, {RPop, "rpop"}, {LRange, "lrange"}, {LLen, "llen"}, {LIndex, "lindex"}, {LSet, "lset"}, {LRem, "lrem"}, {LTrim, "ltrim"}, {LInsertBefore, "linsertbefore"}, {LInsertAfter, "linsertafter"}, {ZAdd, "zadd"}, {ZRem, "zrem"}, {ZScore, "zscore"}, {ZCard, "zcard"}, {ZCount, "zcount"}, {ZIncrBy, "zincrby"}, {ZRange, "zrange"}, {ZRevRange, "zrevrange"}, {ZRangeByScore, "zrangebyscore"}, {ZRevRangeByScore, "zrevrangebyscore"}, {ZRank, "zrank"}, {ZRevRank, "zrevrank"}, {ZPopMin, "zpopmin"}, {ZPopMax, "zpopmax"}, {ZRemRangeByRank, "zremrangebyrank"}, {ZRemRangeByScore, "zremrangebyscore"},
	}
	var out []string
	for _, o := range order {
		if p&o.bit != 0 {
			out = append(out, o.name)
		}
	}
	return out
}
