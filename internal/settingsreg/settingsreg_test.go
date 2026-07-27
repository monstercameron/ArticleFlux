package settingsreg

import (
	"context"
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
