package api

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/doptime/dopdb"
)

type InDemo struct {
	Name string `json:"name"`
}
type UserReq struct{ Name string }
type OrderArg struct{ Name string }
type Foo struct{ Name string }

func TestNameDerivation(t *testing.T) {
	// Type-derived names strip one leading/trailing affix and lower-case.
	typeCases := []struct {
		v    any
		want string
	}{
		{InDemo{}, "demo"},    // strip "In" prefix
		{UserReq{}, "user"},   // strip "Req" suffix
		{OrderArg{}, "order"}, // strip "Arg" suffix
		{Foo{}, "foo"},
	}
	for _, c := range typeCases {
		if got := apiNameByType(reflect.TypeOf(c.v)); got != c.want {
			t.Errorf("apiNameByType(%T)=%q want %q", c.v, got, c.want)
		}
	}
	// Explicit names (WithName) are literal — only lower-cased.
	if got := cleanName("DreamAnalyzer"); got != "dreamanalyzer" {
		t.Errorf("cleanName(DreamAnalyzer)=%q", got)
	}
	if got := cleanName("UserReq"); got != "userreq" {
		t.Errorf("explicit names must NOT be affix-stripped: %q", got)
	}
}

func TestLocalCall(t *testing.T) {
	clearRegistry()
	ep := Api(func(in *InDemo) (string, error) {
		return "hello " + in.Name, nil
	}, WithName("greet"))

	out, err := ep.Func(&InDemo{Name: "Ada"})
	if err != nil || out != "hello Ada" {
		t.Fatalf("local call: out=%q err=%v", out, err)
	}
	if ep.Name != "greet" {
		t.Errorf("name=%q", ep.Name)
	}
}

func TestValidateThenFunc(t *testing.T) {
	clearRegistry()
	var trace []string
	ep := Api(func(in *InDemo) (string, error) {
		trace = append(trace, "func")
		return "R:" + in.Name, nil
	}, WithName("pipe"))
	ep.Validate = func(v any) error {
		trace = append(trace, "validate")
		return nil
	}

	got, err := ep.CallByMap(context.Background(), map[string]any{"name": "x"}, nil)
	if err != nil {
		t.Fatalf("CallByMap: %v", err)
	}
	want := []string{"validate", "func"}
	if strings.Join(trace, ",") != strings.Join(want, ",") {
		t.Errorf("order=%v want %v", trace, want)
	}
	if got != "R:x" { // Func result returned directly (no ResponseModifier)
		t.Errorf("result=%v", got)
	}
}

func TestValidateBlocks(t *testing.T) {
	clearRegistry()
	called := false
	ep := Api(func(in *InDemo) (string, error) {
		called = true
		return "", nil
	}, WithName("guard"))
	ep.Validate = func(v any) error { return errors.New("nope") }

	if _, err := ep.CallByMap(context.Background(), map[string]any{"name": "x"}, nil); err == nil {
		t.Fatal("expected validation error")
	}
	if called {
		t.Error("Func ran despite validation failure")
	}
}

func TestAtContextDecode(t *testing.T) {
	clearRegistry()
	type Scoped struct {
		UID  string `json:"@uid"` // filled from @-context, never the client
		Note string `json:"note"`
	}
	ep := Api(func(in *Scoped) (string, error) {
		return in.UID + ":" + in.Note, nil
	}, WithName("scoped"))

	// Simulates the param map the HTTP layer assembles: body field "note" plus
	// injected @uid from the verified JWT.
	out, err := ep.CallByMap(context.Background(), map[string]any{
		"note":  "hi",
		"@uid":  "u1",
		"@key":  "scoped",
		"other": "ignored",
	}, nil)
	if err != nil {
		t.Fatalf("CallByMap: %v", err)
	}
	if out != "u1:hi" {
		t.Errorf("out=%q want u1:hi", out)
	}
}

func TestValidatorFromFramework(t *testing.T) {
	clearRegistry()
	dopdb.SetValidator(func(v any) error {
		if s, ok := v.(*InDemo); ok && s.Name == "" {
			return errors.New("name required")
		}
		return nil
	})
	defer dopdb.SetValidator(nil)

	ep := Api(func(in *InDemo) (string, error) { return in.Name, nil }, WithName("fwval"))
	if _, err := ep.CallByMap(context.Background(), map[string]any{}, nil); err == nil {
		t.Fatal("expected framework validator to reject empty name")
	}
	if _, err := ep.CallByMap(context.Background(), map[string]any{"name": "ok"}, nil); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
}

func TestCallByNameNotFound(t *testing.T) {
	clearRegistry()
	if _, err := CallByName(context.Background(), "ghost", nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}
