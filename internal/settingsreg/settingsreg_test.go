package settingsreg

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// fakeStore is the storage interface, in memory. The precedence logic is the
// part worth testing and it has nothing to do with SQL.
type fakeStore struct {
	rows map[store.SettingLayer]map[string]string
}

func newFake() *fakeStore {
	return &fakeStore{rows: map[store.SettingLayer]map[string]string{
		store.LayerUser: {}, store.LayerTenant: {}, store.LayerSystem: {},
	}}
}

func (f *fakeStore) ResolveSettings(_ context.Context, _ store.Scope, keys []string) (map[string]map[store.SettingLayer]string, error) {
	out := map[string]map[store.SettingLayer]string{}
	for _, k := range keys {
		for layer, m := range f.rows {
			if v, ok := m[k]; ok {
				if out[k] == nil {
					out[k] = map[store.SettingLayer]string{}
				}
				out[k][layer] = v
			}
		}
	}
	return out, nil
}

func (f *fakeStore) SetUserSetting(_ context.Context, _ store.Scope, key, v string) error {
	f.rows[store.LayerUser][key] = v
	return nil
}

func (f *fakeStore) SetTenantSetting(_ context.Context, _ store.Scope, key, v string) error {
	f.rows[store.LayerTenant][key] = v
	return nil
}

func (f *fakeStore) ClearUserSetting(_ context.Context, _ store.Scope, key string) error {
	delete(f.rows[store.LayerUser], key)
	return nil
}

var sc = store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r := New()
	r.MustRegister(
		Def{Key: "reading.font_size", Kind: KindInt, Default: 16, Scope: ScopeUser,
			Min: 10, Max: 32, Doc: "Reading pane font size in pixels."},
		Def{Key: "reading.theme", Kind: KindEnum, Default: "auto", Scope: ScopeUser,
			Enum: []string{"auto", "light", "dark"}, Doc: "Colour theme."},
		Def{Key: "smart.enabled", Kind: KindBool, Default: false, Scope: ScopeUser,
			Doc: "Whether Smart ranking is on."},
		Def{Key: "retention.days", Kind: KindInt, Default: 365, Scope: ScopeTenant,
			Min: 7, Max: 3650, Doc: "How long items are kept."},
		Def{Key: "smart.model", Kind: KindString, Default: "", Scope: ScopeSystem,
			Doc: "The Smart+ model id."},
		Def{Key: "reading.zoom", Kind: KindFloat, Default: 1.0, Scope: ScopeUser,
			Doc: "Reading pane zoom factor."},
		Def{Key: "retention.multiplier", Kind: KindFloat, Default: 1.0, Scope: ScopeTenant,
			Doc: "Retention scaling factor."},
	)
	return r
}

func resolve(t *testing.T, r *Registry, f *fakeStore) map[string]Value {
	t.Helper()
	vals, problems, err := r.Resolve(context.Background(), f, sc)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Logf("problem: %v", p)
	}
	return vals
}

// The registry's reason for existing: user beats tenant beats system beats the
// registered default, and the LAYER comes back too.
func TestPrecedenceAndProvenance(t *testing.T) {
	r := testRegistry(t)
	f := newFake()

	t.Run("nothing stored yields the default", func(t *testing.T) {
		v := resolve(t, r, f)["reading.font_size"]
		if v.Int() != 16 {
			t.Errorf("= %d, want the default 16", v.Int())
		}
		if v.Layer != store.LayerDefault {
			t.Errorf("layer = %s", v.Layer)
		}
	})

	t.Run("system beats the default", func(t *testing.T) {
		f.rows[store.LayerSystem]["reading.font_size"] = "18"
		v := resolve(t, r, f)["reading.font_size"]
		if v.Int() != 18 || v.Layer != store.LayerSystem {
			t.Errorf("= %d at %s", v.Int(), v.Layer)
		}
	})

	t.Run("tenant beats system", func(t *testing.T) {
		f.rows[store.LayerTenant]["reading.font_size"] = "20"
		v := resolve(t, r, f)["reading.font_size"]
		if v.Int() != 20 || v.Layer != store.LayerTenant {
			t.Errorf("= %d at %s", v.Int(), v.Layer)
		}
	})

	t.Run("user beats tenant", func(t *testing.T) {
		f.rows[store.LayerUser]["reading.font_size"] = "24"
		v := resolve(t, r, f)["reading.font_size"]
		if v.Int() != 24 || v.Layer != store.LayerUser {
			t.Errorf("= %d at %s", v.Int(), v.Layer)
		}
	})

	// "Why is this off for me?" has two very different answers, and a settings
	// screen that cannot tell them apart shows a control that silently does
	// nothing.
	t.Run("resetting a user override falls back and says so", func(t *testing.T) {
		if err := r.ResetUser(context.Background(), f, sc, "reading.font_size"); err != nil {
			t.Fatal(err)
		}
		v := resolve(t, r, f)["reading.font_size"]
		if v.Int() != 20 || v.Layer != store.LayerTenant {
			t.Errorf("after reset = %d at %s, want the tenant's 20", v.Int(), v.Layer)
		}
	})
}

// A typo stored as a loose string reads as absent and looks like a setting that
// does not work.
func TestUnregisteredKeysAreRefused(t *testing.T) {
	r := testRegistry(t)
	f := newFake()

	err := r.SetUser(context.Background(), f, sc, "reading.font_sze", 18)
	if err == nil {
		t.Fatal("a misspelt key was accepted")
	}
	if !strings.Contains(err.Error(), "not a registered setting") {
		t.Errorf("error = %v", err)
	}
	if len(f.rows[store.LayerUser]) != 0 {
		t.Error("the misspelt key was written anyway")
	}
}

// A setting whose scope is tenant-or-above must not be overridable per user;
// that is how "the admin decides" is expressed structurally rather than by
// hoping the UI hides the control.
func TestScopeLimitsAreEnforced(t *testing.T) {
	r := testRegistry(t)
	f := newFake()
	ctx := context.Background()

	if err := r.SetUser(ctx, f, sc, "retention.days", 30); err == nil {
		t.Error("a tenant-scoped setting was written at the user layer")
	}
	if err := r.SetTenant(ctx, f, sc, "retention.days", 30); err != nil {
		t.Errorf("a tenant-scoped setting was refused at the tenant layer: %v", err)
	}
	if err := r.SetTenant(ctx, f, sc, "smart.model", "gpt-x"); err == nil {
		t.Error("instance configuration was written at the tenant layer")
	}
}

func TestValidation(t *testing.T) {
	r := testRegistry(t)
	f := newFake()
	ctx := context.Background()

	cases := []struct {
		name  string
		key   string
		value any
		ok    bool
	}{
		{"int in range", "reading.font_size", 18, true},
		{"int below min", "reading.font_size", 4, false},
		{"int above max", "reading.font_size", 200, false},
		{"wrong type for int", "reading.font_size", "large", false},
		{"fractional value for an int", "reading.font_size", 16.5, false},
		{"valid enum", "reading.theme", "dark", true},
		{"invalid enum", "reading.theme", "sepia", false},
		{"wrong type for enum", "reading.theme", 3, false},
		{"bool", "smart.enabled", true, true},
		{"wrong type for bool", "smart.enabled", "yes", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := r.SetUser(ctx, f, sc, c.key, c.value)
			if c.ok && err != nil {
				t.Errorf("rejected a valid value: %v", err)
			}
			if !c.ok && err == nil {
				t.Error("accepted an invalid value")
			}
		})
	}

	// The message has to say what is allowed, or the reader is guessing.
	err := r.SetUser(ctx, f, sc, "reading.theme", "sepia")
	if err == nil || !strings.Contains(err.Error(), "auto") {
		t.Errorf("the enum error does not list the options: %v", err)
	}
	err = r.SetUser(ctx, f, sc, "reading.font_size", 200)
	if err == nil || !strings.Contains(err.Error(), "32") {
		t.Errorf("the range error does not give the bound: %v", err)
	}
}

// A corrupt stored value must not blank a settings screen: the other
// eighty-nine settings are fine and the reader needs to see them.
func TestACorruptValueIsSkippedAndReported(t *testing.T) {
	r := testRegistry(t)
	f := newFake()

	f.rows[store.LayerSystem]["reading.font_size"] = "18"
	f.rows[store.LayerUser]["reading.font_size"] = `"enormous"` // wrong type
	f.rows[store.LayerUser]["reading.theme"] = `"dark"`         // fine

	vals, problems, err := r.Resolve(context.Background(), f, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) == 0 {
		t.Error("a corrupt value was accepted silently")
	}

	// Resolution CONTINUES past the bad layer rather than stopping, or a bad
	// user value would mask a good system one.
	if got := vals["reading.font_size"]; got.Int() != 18 || got.Layer != store.LayerSystem {
		t.Errorf("font size = %d at %s; the corrupt user value should have been skipped "+
			"and the system value used", got.Int(), got.Layer)
	}
	// And everything else still resolves.
	if got := vals["reading.theme"]; got.String() != "dark" {
		t.Errorf("an unrelated setting was affected: %q", got.String())
	}
	if len(vals) != len(r.Keys()) {
		t.Errorf("resolved %d of %d settings", len(vals), len(r.Keys()))
	}
}

// A default outside its own bounds is a bug that should not survive startup.
func TestRegistrationValidatesItsOwnDefaults(t *testing.T) {
	cases := []struct {
		name string
		def  Def
	}{
		{"default below min", Def{Key: "a", Kind: KindInt, Default: 1, Min: 10, Max: 20, Doc: "x"}},
		{"enum default not in the enum", Def{Key: "b", Kind: KindEnum, Default: "purple",
			Enum: []string{"red", "blue"}, Doc: "x"}},
		{"default of the wrong type", Def{Key: "c", Kind: KindBool, Default: "true", Doc: "x"}},
		{"no key", Def{Kind: KindBool, Default: true, Doc: "x"}},
		// Appendix B is generated from these; an undocumented setting is one
		// nobody can decide whether to change.
		{"no documentation", Def{Key: "d", Kind: KindBool, Default: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := New().Register(c.def); err == nil {
				t.Error("an invalid definition was accepted")
			}
		})
	}

	r := New()
	good := Def{Key: "a", Kind: KindInt, Default: 5, Doc: "x"}
	if err := r.Register(good); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(good); err == nil {
		t.Error("the same key was registered twice")
	}
}

// json.Unmarshal into `any` makes every number a float64, so an int setting
// arrives as a float. Without converting back, every stored integer fails its
// own type check — which would make the whole registry appear corrupt.
func TestStoredIntegersValidateAsIntegers(t *testing.T) {
	r := testRegistry(t)
	f := newFake()
	f.rows[store.LayerUser]["reading.font_size"] = "18"

	vals, problems, err := r.Resolve(context.Background(), f, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Errorf("a perfectly good stored integer was reported as a problem: %v", problems)
	}
	if vals["reading.font_size"].Int() != 18 {
		t.Errorf("= %d", vals["reading.font_size"].Int())
	}
}

// MustRegister exists for a package-level table, where an invalid definition is a
// programming error that should stop the process rather than be silently ignored.
func TestMustRegisterPanicsOnAnInvalidDefinition(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustRegister did not panic on an invalid definition")
		}
	}()
	New().MustRegister(Def{Key: "a", Kind: KindBool, Default: "not a bool", Doc: "x"})
}

func TestDefLookup(t *testing.T) {
	r := testRegistry(t)

	d, ok := r.Def("reading.font_size")
	if !ok {
		t.Fatal("a registered key was not found")
	}
	if d.Kind != KindInt || d.Default != 16 {
		t.Errorf("Def = %+v; wrong definition returned", d)
	}

	if _, ok := r.Def("no.such.key"); ok {
		t.Error("an unregistered key was reported as found")
	}
}

// Bool/Int/Float/String all share the same contract: parse the stored JSON at the
// registered type, or fall back to the definition's own default on a mismatch — a
// corrupt value must not take a screen down.
func TestValueAccessorsFallBackToDefaultOnAMismatch(t *testing.T) {
	t.Run("Bool", func(t *testing.T) {
		def := Def{Kind: KindBool, Default: true}
		if v := (Value{Raw: "true", def: def}); !v.Bool() {
			t.Error("valid JSON true did not parse")
		}
		if v := (Value{Raw: "false", def: def}); v.Bool() {
			t.Error("valid JSON false did not parse")
		}
		if v := (Value{Raw: `"not a bool"`, def: def}); !v.Bool() {
			t.Error("a type-mismatched raw value should fall back to the default (true)")
		}
	})
	t.Run("Int", func(t *testing.T) {
		def := Def{Kind: KindInt, Default: 42}
		if v := (Value{Raw: "7", def: def}); v.Int() != 7 {
			t.Errorf("= %d, want 7", v.Int())
		}
		if v := (Value{Raw: `"seven"`, def: def}); v.Int() != 42 {
			t.Errorf("= %d, want the default 42", v.Int())
		}
	})
	t.Run("Float", func(t *testing.T) {
		def := Def{Kind: KindFloat, Default: 1.5}
		if v := (Value{Raw: "3.25", def: def}); v.Float() != 3.25 {
			t.Errorf("= %v, want 3.25", v.Float())
		}
		if v := (Value{Raw: "not-json", def: def}); v.Float() != 1.5 {
			t.Errorf("= %v, want the default 1.5", v.Float())
		}
	})
	t.Run("String", func(t *testing.T) {
		def := Def{Kind: KindString, Default: "fallback"}
		if v := (Value{Raw: `"hello"`, def: def}); v.String() != "hello" {
			t.Errorf("= %q, want %q", v.String(), "hello")
		}
		if v := (Value{Raw: "not-json", def: def}); v.String() != "fallback" {
			t.Errorf("= %q, want the default %q", v.String(), "fallback")
		}
	})
}

// A registered default that cannot itself be marshalled to JSON (Inf, in practice —
// Register's own type/range checks let it through since range bounds of 0/0 mean
// "unbounded") must be reported as a problem rather than resolved to garbage, and the
// key is left out of the result entirely rather than handed back half-formed.
func TestResolveReportsAnUnserialisableDefault(t *testing.T) {
	r := New()
	r.MustRegister(
		Def{Key: "broken.default", Kind: KindFloat, Default: math.Inf(1), Doc: "x"},
		Def{Key: "fine.one", Kind: KindInt, Default: 5, Doc: "x"},
	)
	f := newFake()
	vals, problems, err := r.Resolve(context.Background(), f, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) == 0 {
		t.Fatal("an unserialisable default was not reported")
	}
	if _, ok := vals["broken.default"]; ok {
		t.Error("a key whose default failed to serialise should not appear in the result")
	}
	if vals["fine.one"].Int() != 5 {
		t.Errorf("an unrelated key was affected: %+v", vals["fine.one"])
	}
}

// SetTenant validates its value and can fail to marshal it, exactly like SetUser —
// both were only exercised for the scope-limit checks before this.
func TestSetTenantValidatesAndCanFailToMarshal(t *testing.T) {
	r := testRegistry(t)
	f := newFake()
	ctx := context.Background()

	if err := r.SetTenant(ctx, f, sc, "retention.days", "not a number"); err == nil {
		t.Error("an invalid value was accepted at the tenant layer")
	}
	if err := r.SetTenant(ctx, f, sc, "retention.multiplier", math.Inf(1)); err == nil {
		t.Error("an unserialisable value (+Inf) was accepted")
	} else if !strings.Contains(err.Error(), "Inf") && !strings.Contains(strings.ToLower(err.Error()), "json") {
		t.Logf("marshal error (informational): %v", err)
	}
}

// ResetUser is a registered-key lookup like every other entry point; an unregistered
// key must be refused there too, not just on the write side.
func TestResetUserRefusesAnUnregisteredKey(t *testing.T) {
	r := testRegistry(t)
	f := newFake()
	if err := r.ResetUser(context.Background(), f, sc, "no.such.key"); err == nil {
		t.Fatal("ResetUser accepted an unregistered key")
	}
}

// A setting scoped above the user layer must be refused at SetUser regardless of
// which layer it is scoped to — both the tenant and the system case go through the
// same message, naming the actual owning layer.
func TestSetUserRefusesSystemScopedSettingsToo(t *testing.T) {
	r := testRegistry(t)
	f := newFake()
	err := r.SetUser(context.Background(), f, sc, "smart.model", "gpt-x")
	if err == nil {
		t.Fatal("a system-scoped setting was written at the user layer")
	}
	if !strings.Contains(err.Error(), "system") {
		t.Errorf("error %q does not name the system layer", err)
	}
}

// toFloat accepts every Go numeric type that could plausibly arrive from a caller
// (int and float64 are exercised elsewhere; int64 and float32 are the other two a Go
// caller can legitimately hand in).
func TestValidateAcceptsEveryNumericGoType(t *testing.T) {
	r := testRegistry(t)
	f := newFake()
	ctx := context.Background()

	if err := r.SetUser(ctx, f, sc, "reading.zoom", int64(2)); err != nil {
		t.Errorf("int64 was rejected for a float setting: %v", err)
	}
	if err := r.SetUser(ctx, f, sc, "reading.zoom", float32(1.25)); err != nil {
		t.Errorf("float32 was rejected for a float setting: %v", err)
	}
	if err := r.SetUser(ctx, f, sc, "reading.zoom", true); err == nil {
		t.Error("a bool was accepted for a float setting")
	}
}

// SetUser has the same marshal-failure branch as SetTenant, reached only once
// validate has already let the value through — Inf clears an unbounded (0/0) range
// check but still cannot be marshalled to JSON.
func TestSetUserFailsOnAnUnmarshallableValue(t *testing.T) {
	r := testRegistry(t)
	f := newFake()
	if err := r.SetUser(context.Background(), f, sc, "reading.zoom", math.Inf(1)); err == nil {
		t.Fatal("+Inf was accepted despite being unmarshallable to JSON")
	}
	if len(f.rows[store.LayerUser]) != 0 {
		t.Error("a value that failed to marshal was written anyway")
	}
}

// validate's unknown-kind branch and the string type-mismatch branch, neither of
// which any existing case exercised: Register itself validates its own default, so
// an unknown Kind is caught at registration before it ever reaches storage.
func TestValidateRejectsUnknownKindsAndWrongStringTypes(t *testing.T) {
	if err := New().Register(Def{Key: "x", Kind: Kind("mystery"), Default: "y", Doc: "d"}); err == nil {
		t.Fatal("an unknown Kind was accepted at registration")
	} else if !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("error %q does not mention the unknown kind", err)
	}

	r := New()
	r.MustRegister(Def{Key: "s", Kind: KindString, Default: "x", Doc: "d"})
	if err := r.SetUser(context.Background(), newFake(), sc, "s", 42); err == nil {
		t.Error("a non-string value was accepted for a KindString setting")
	}
}

// validateRaw's own two failure modes, distinct from validate's: JSON that will not
// parse at all, and a stored int whose JSON type is not a number (json.Unmarshal into
// `any` gives float64 for numbers, so anything else — a string, here — means the
// stored value was never a valid int to begin with).
func TestValidateRawRejectsMalformedAndWrongTypedStorage(t *testing.T) {
	r := testRegistry(t)
	f := newFake()
	f.rows[store.LayerUser]["reading.font_size"] = "{not json"
	f.rows[store.LayerTenant]["retention.days"] = `"365"` // a JSON string, not a number

	vals, problems, err := r.Resolve(context.Background(), f, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) < 2 {
		t.Fatalf("got %d problems, want at least 2: %v", len(problems), problems)
	}
	// Both corrupt layers were skipped, so both settings fall back to their defaults.
	if vals["reading.font_size"].Int() != 16 {
		t.Errorf("font_size = %d, want the default 16", vals["reading.font_size"].Int())
	}
	if vals["retention.days"].Int() != 365 {
		t.Errorf("retention.days = %d, want the default 365", vals["retention.days"].Int())
	}
}

// Every registered setting resolves, so a settings screen asking for all of them
// gets all of them in one call.
func TestResolveReturnsEveryRegisteredKey(t *testing.T) {
	r := testRegistry(t)
	vals := resolve(t, r, newFake())
	for _, key := range r.Keys() {
		if _, ok := vals[key]; !ok {
			t.Errorf("%q did not resolve", key)
		}
	}
}
