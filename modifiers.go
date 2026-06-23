package dopdb

import (
	"crypto/rand"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ----------------------------------------------------------------------------
// Write-time preprocessing.
//
// redisdb ran modifiers / timestamps / validation on the READ path, and only
// for HTTP traffic — so UpdatedAt actually meant "last served", invalid data
// could be written and only blew up on read, and a direct Go call behaved
// differently from an HTTP call. dopdb runs all of it once, on WRITE, on every
// path. That is the single most important behavioural fix carried over from the
// earlier review.
// ----------------------------------------------------------------------------

// Validator is a pluggable validation hook (default: none). Wire
// go-playground/validator here in your binary to honour `validate:"..."` tags:
//
//	dopdb.SetValidator(func(v any) error { return validate.Struct(v) })
var globalValidator func(v any) error

// SetValidator installs the process-wide validation function used on write.
func SetValidator(fn func(v any) error) { globalValidator = fn }

// RunValidate runs the process-wide validator (if any) against v. The api
// package uses this so the whole framework has a single validation config.
func RunValidate(v any) error {
	if globalValidator != nil {
		return globalValidator(v)
	}
	return nil
}

// modStep is one parsed `mod:` directive on a field.
type modStep struct {
	name  string
	arg   string
	force bool
}

// fieldMods is the precomputed modifier plan for a single struct field.
type fieldMods struct {
	index int
	steps []modStep
}

// writePlan is the precomputed preprocessing plan for a value type V,
// built once in New and reused on every write.
type writePlan struct {
	isPtr        bool
	structType   reflect.Type // the underlying struct type (V or *V's elem)
	mods         []fieldMods
	createdAtIdx int
	updatedAtIdx int
	hasWork      bool
}

func buildWritePlan(vType reflect.Type) *writePlan {
	p := &writePlan{createdAtIdx: -1, updatedAtIdx: -1}
	t := vType
	for t.Kind() == reflect.Ptr {
		p.isPtr = true
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return p // nothing to preprocess for scalar value types
	}
	p.structType = t

	timeType := reflect.TypeOf(time.Time{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		if f.Type == timeType {
			switch f.Name {
			case "CreatedAt":
				p.createdAtIdx = i
			case "UpdatedAt":
				p.updatedAtIdx = i
			}
		}
		if tag := f.Tag.Get("mod"); tag != "" {
			steps := parseMods(tag)
			if len(steps) > 0 {
				p.mods = append(p.mods, fieldMods{index: i, steps: steps})
			}
		}
	}
	p.hasWork = len(p.mods) > 0 || p.createdAtIdx >= 0 || p.updatedAtIdx >= 0
	return p
}

func parseMods(tag string) []modStep {
	var out []modStep
	for _, raw := range strings.Split(tag, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// trailing ",force" attaches to the previous directive in redisdb's
		// grammar; here we accept it as a standalone token applying to the
		// preceding step.
		if raw == "force" {
			if n := len(out); n > 0 {
				out[n-1].force = true
			}
			continue
		}
		step := modStep{}
		if eq := strings.IndexByte(raw, '='); eq >= 0 {
			step.name = raw[:eq]
			step.arg = raw[eq+1:]
		} else {
			step.name = raw
		}
		out = append(out, step)
	}
	return out
}

// apply runs the write plan against v (a value of type V). It returns the
// possibly-modified value to be persisted. For pointer V the receiver struct is
// mutated in place; for struct V a mutable copy is made.
func (p *writePlan) apply(v any) (any, error) {
	if !p.hasWork {
		if globalValidator != nil {
			if err := globalValidator(v); err != nil {
				return v, err
			}
		}
		return v, nil
	}

	var sv reflect.Value
	if p.isPtr {
		rv := reflect.ValueOf(v)
		if rv.IsNil() {
			return v, nil
		}
		sv = rv.Elem()
	} else {
		// addressable copy
		cp := reflect.New(p.structType)
		cp.Elem().Set(reflect.ValueOf(v))
		sv = cp.Elem()
		defer func() { v = sv.Interface() }()
	}

	for _, fm := range p.mods {
		field := sv.Field(fm.index)
		if !field.CanSet() {
			continue
		}
		for _, st := range fm.steps {
			applyStep(field, st)
		}
	}

	now := time.Now().UTC()
	if p.createdAtIdx >= 0 {
		f := sv.Field(p.createdAtIdx)
		if f.CanSet() && f.Interface().(time.Time).IsZero() {
			f.Set(reflect.ValueOf(now))
		}
	}
	if p.updatedAtIdx >= 0 {
		f := sv.Field(p.updatedAtIdx)
		if f.CanSet() {
			f.Set(reflect.ValueOf(now)) // UpdatedAt always set on write
		}
	}

	out := v
	if !p.isPtr {
		out = sv.Interface()
	}
	if globalValidator != nil {
		if err := globalValidator(out); err != nil {
			return out, err
		}
	}
	return out, nil
}

func applyStep(field reflect.Value, st modStep) {
	isZero := field.IsZero()
	// Value-fill directives only fire on zero unless ",force".
	switch st.name {
	case "trim":
		if field.Kind() == reflect.String {
			field.SetString(strings.TrimSpace(field.String()))
		}
	case "lowercase":
		if field.Kind() == reflect.String {
			field.SetString(strings.ToLower(field.String()))
		}
	case "uppercase":
		if field.Kind() == reflect.String {
			field.SetString(strings.ToUpper(field.String()))
		}
	case "title":
		if field.Kind() == reflect.String {
			field.SetString(strings.Title(strings.ToLower(field.String()))) //nolint:staticcheck
		}
	case "default":
		if isZero || st.force {
			setScalarFromString(field, st.arg)
		}
	case "unixtime":
		if isZero || st.force {
			now := time.Now()
			if st.arg == "ms" {
				setInt(field, now.UnixMilli())
			} else {
				setInt(field, now.Unix())
			}
		}
	case "counter":
		incNumeric(field)
	case "nanoid":
		if isZero || st.force {
			n := 21
			if st.arg != "" {
				if m, err := strconv.Atoi(st.arg); err == nil && m > 0 {
					n = m
				}
			}
			if field.Kind() == reflect.String {
				field.SetString(nanoID(n))
			}
		}
	}
}

func setScalarFromString(field reflect.Value, s string) {
	switch field.Kind() {
	case reflect.String:
		field.SetString(s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			field.SetInt(n)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			field.SetUint(n)
		}
	case reflect.Float32, reflect.Float64:
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			field.SetFloat(n)
		}
	case reflect.Bool:
		if b, err := strconv.ParseBool(s); err == nil {
			field.SetBool(b)
		}
	}
}

func setInt(field reflect.Value, n int64) {
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.SetUint(uint64(n))
	case reflect.Float32, reflect.Float64:
		field.SetFloat(float64(n))
	}
}

func incNumeric(field reflect.Value) {
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(field.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.SetUint(field.Uint() + 1)
	case reflect.Float32, reflect.Float64:
		field.SetFloat(field.Float() + 1)
	}
}

const nanoAlphabet = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict"

func nanoID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is fatal in practice; fall back to time-seeded bytes
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i % 8))
		}
	}
	for i := range b {
		b[i] = nanoAlphabet[int(b[i])&63]
	}
	return string(b)
}
