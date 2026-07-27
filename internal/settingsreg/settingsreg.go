// Package settingsreg is the typed settings registry (TODO 6.3, plan.md §6.3,
// Appendix B).
//
// # Three things a map[string]string cannot do
//
// The registry exists because ~90 settings stored as loose strings produce three
// failures that only show up in use:
//
//  1. **A typo is a silent default.** `reading.font_size` misspelt reads as
//     absent, falls back to the default, and looks like a setting that does not
//     work. Registered keys make it an error at the boundary instead.
//  2. **A default written in two places drifts.** The server's fallback and the
//     client's placeholder disagree, and the screen shows one number while the
//     behaviour uses another. There is one definition here and everything reads
//     it.
//  3. **"Why is this off for me?" is unanswerable.** Resolution returns the
//     LAYER as well as the value, so the UI can say "your admin disabled this"
//     rather than showing a control that silently does nothing.
//
// # Precedence: user, then tenant, then system, then the registered default
//
// First hit wins. The layer is reported so a settings screen can distinguish an
// inherited value from an override, which is what makes "reset to default" a
// meaningful button rather than a write of the current value.
package settingsreg

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Kind is a setting's type.
type Kind string

const (
	KindBool   Kind = "bool"
	KindInt    Kind = "int"
	KindFloat  Kind = "float"
	KindString Kind = "string"
	KindEnum   Kind = "enum"
)

// Scope is the lowest layer at which a setting may be written.
//
// A setting whose ScopeLimit is Tenant cannot be overridden per user. That is
// how "the admin decides the retention period" is expressed structurally rather
// than by hoping the UI does not offer the control.
type Scope int

const (
	// ScopeUser means anyone may override it for themselves.
	ScopeUser Scope = iota
	// ScopeTenant means only a tenant admin may set it.
	ScopeTenant
	// ScopeSystem means only the operator, in instance configuration.
	ScopeSystem
)

// Def is one registered setting.
type Def struct {
	Key  string
	Kind Kind
	// Default is the value when no layer has one. Stored as its Go type so the
	// registry can validate it against Kind at registration rather than at first
	// read — a default that does not match its own type is a bug that should not
	// survive startup.
	Default any
	// Scope is the lowest layer at which this may be written.
	Scope Scope
	// Enum lists the permitted values for KindEnum.
	Enum []string
	// Min and Max bound numeric settings. Both zero means unbounded.
	Min, Max float64
	// Doc is the one-line meaning, kept beside the default so the two cannot
	// drift apart.
	Doc string
}

// Registry holds the definitions.
type Registry struct {
	defs map[string]Def
}

// New builds an empty registry.
func New() *Registry { return &Registry{defs: map[string]Def{}} }

// Register adds a definition, returning an error rather than panicking.
//
// Validated at registration: a default outside its own bounds, or an enum
// default not in its own list, is caught at startup rather than at the first
// read by the one user who has not overridden it.
func (r *Registry) Register(d Def) error {
	if d.Key == "" {
		return fmt.Errorf("settingsreg: a setting needs a key")
	}
	if _, dup := r.defs[d.Key]; dup {
		return fmt.Errorf("settingsreg: %q is registered twice", d.Key)
	}
	if d.Doc == "" {
		// Not pedantry: Appendix B is generated from these, and an undocumented
		// setting is one nobody can decide whether to change.
		return fmt.Errorf("settingsreg: %q has no documentation", d.Key)
	}
	if err := validate(d, d.Default); err != nil {
		return fmt.Errorf("settingsreg: %q has an invalid default: %w", d.Key, err)
	}
	r.defs[d.Key] = d
	return nil
}

// MustRegister is Register for a package-level table, where a failure is a
// programming error that should stop the process rather than be handled.
func (r *Registry) MustRegister(defs ...Def) {
	for _, d := range defs {
		if err := r.Register(d); err != nil {
			panic(err)
		}
	}
}

// Keys lists every registered key, sorted.
func (r *Registry) Keys() []string {
	out := make([]string, 0, len(r.defs))
	for k := range r.defs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Def returns a definition.
func (r *Registry) Def(key string) (Def, bool) {
	d, ok := r.defs[key]
	return d, ok
}

// Value is a resolved setting.
type Value struct {
	Key string
	// Raw is the JSON as stored, or the default rendered as JSON.
	Raw string
	// Layer says which layer supplied it, which is what lets the UI explain
	// itself rather than showing a control that does nothing.
	Layer store.SettingLayer
	def   Def
}

// Bool, Int, Float and String read a value at its registered type.
//
// They return the DEFAULT rather than an error on a type mismatch, and that is
// deliberate: a corrupt stored value must not take a screen down. The mismatch
// is reported by Resolve's error slice, which the caller can log, while the
// application keeps working with a value it knows is valid.
func (v Value) Bool() bool {
	var b bool
	if json.Unmarshal([]byte(v.Raw), &b) == nil {
		return b
	}
	d, _ := v.def.Default.(bool)
	return d
}

func (v Value) Int() int {
	var n int
	if json.Unmarshal([]byte(v.Raw), &n) == nil {
		return n
	}
	d, _ := v.def.Default.(int)
	return d
}

func (v Value) Float() float64 {
	var f float64
	if json.Unmarshal([]byte(v.Raw), &f) == nil {
		return f
	}
	d, _ := v.def.Default.(float64)
	return d
}

func (v Value) String() string {
	var s string
	if json.Unmarshal([]byte(v.Raw), &s) == nil {
		return s
	}
	d, _ := v.def.Default.(string)
	return d
}

// Reader is the storage this needs. An interface so the registry is testable
// without a database — the precedence logic is the part worth testing, and it
// has nothing to do with SQL.
type Reader interface {
	ResolveSettings(ctx context.Context, s store.Scope, keys []string) (map[string]map[store.SettingLayer]string, error)
}

// Resolve reads every registered setting for one caller.
//
// Returns the values plus any problems found. Problems are returned rather than
// erroring the call because one corrupt row must not blank a settings screen —
// the other eighty-nine settings are fine and the reader needs to see them.
func (r *Registry) Resolve(ctx context.Context, db Reader, sc store.Scope) (map[string]Value, []error, error) {
	keys := r.Keys()
	stored, err := db.ResolveSettings(ctx, sc, keys)
	if err != nil {
		return nil, nil, err
	}

	out := make(map[string]Value, len(keys))
	var problems []error

	for _, key := range keys {
		def := r.defs[key]
		v := Value{Key: key, Layer: store.LayerDefault, def: def}

		// Precedence. First hit wins, most specific first.
		for _, layer := range []store.SettingLayer{store.LayerUser, store.LayerTenant, store.LayerSystem} {
			raw, ok := stored[key][layer]
			if !ok {
				continue
			}
			if err := validateRaw(def, raw); err != nil {
				// A stored value that does not match its definition is skipped
				// and reported, and resolution CONTINUES to the next layer. The
				// alternative — using it — means a bad tenant value silently
				// overrides a good system one.
				problems = append(problems, fmt.Errorf("%s at the %s layer: %w", key, layer, err))
				continue
			}
			v.Raw, v.Layer = raw, layer
			break
		}

		if v.Layer == store.LayerDefault {
			raw, err := json.Marshal(def.Default)
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: default is not serialisable: %w", key, err))
				continue
			}
			v.Raw = string(raw)
		}
		out[key] = v
	}
	return out, problems, nil
}

// Writer is the storage needed to write.
type Writer interface {
	SetUserSetting(ctx context.Context, s store.Scope, key, valueJSON string) error
	SetTenantSetting(ctx context.Context, s store.Scope, key, valueJSON string) error
	ClearUserSetting(ctx context.Context, s store.Scope, key string) error
}

// SetUser writes a user-layer override after validating it.
//
// Validation here rather than only in the UI, because the UI is one of several
// callers: the sync API and the CLI both write settings, and a check that lives
// in a form is a check the other two do not have.
func (r *Registry) SetUser(ctx context.Context, db Writer, sc store.Scope, key string, value any) error {
	def, ok := r.defs[key]
	if !ok {
		// An unregistered key is the typo case, and accepting it would store a
		// value nothing ever reads — which looks exactly like a setting that
		// does not work.
		return fmt.Errorf("settingsreg: %q is not a registered setting", key)
	}
	if def.Scope != ScopeUser {
		return fmt.Errorf("settingsreg: %q is set at the %s layer, not per user", key, scopeName(def.Scope))
	}
	if err := validate(def, value); err != nil {
		return fmt.Errorf("settingsreg: %q: %w", key, err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.SetUserSetting(ctx, sc, key, string(raw))
}

// SetTenant writes a tenant-layer value. The caller is responsible for having
// checked that this user may administer the tenant.
func (r *Registry) SetTenant(ctx context.Context, db Writer, sc store.Scope, key string, value any) error {
	def, ok := r.defs[key]
	if !ok {
		return fmt.Errorf("settingsreg: %q is not a registered setting", key)
	}
	if def.Scope == ScopeSystem {
		return fmt.Errorf("settingsreg: %q is instance configuration, not a tenant setting", key)
	}
	if err := validate(def, value); err != nil {
		return fmt.Errorf("settingsreg: %q: %w", key, err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.SetTenantSetting(ctx, sc, key, string(raw))
}

// ResetUser removes a user's override so the value falls back.
func (r *Registry) ResetUser(ctx context.Context, db Writer, sc store.Scope, key string) error {
	if _, ok := r.defs[key]; !ok {
		return fmt.Errorf("settingsreg: %q is not a registered setting", key)
	}
	return db.ClearUserSetting(ctx, sc, key)
}

func scopeName(s Scope) string {
	switch s {
	case ScopeUser:
		return "user"
	case ScopeTenant:
		return "tenant"
	default:
		return "system"
	}
}

// validate checks a Go value against a definition.
func validate(d Def, value any) error {
	switch d.Kind {
	case KindBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected a boolean, got %T", value)
		}
	case KindInt:
		n, err := toFloat(value)
		if err != nil {
			return fmt.Errorf("expected a whole number, got %T", value)
		}
		if n != float64(int(n)) {
			return fmt.Errorf("expected a whole number, got %v", value)
		}
		return inRange(d, n)
	case KindFloat:
		n, err := toFloat(value)
		if err != nil {
			return fmt.Errorf("expected a number, got %T", value)
		}
		return inRange(d, n)
	case KindString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected a string, got %T", value)
		}
	case KindEnum:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected one of %s, got %T", strings.Join(d.Enum, ", "), value)
		}
		for _, allowed := range d.Enum {
			if s == allowed {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s, got %q", strings.Join(d.Enum, ", "), s)
	default:
		return fmt.Errorf("unknown kind %q", d.Kind)
	}
	return nil
}

// validateRaw checks a stored JSON value.
func validateRaw(d Def, raw string) error {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return fmt.Errorf("stored value is not JSON: %w", err)
	}
	// json.Unmarshal into `any` gives float64 for every number, so an int
	// setting arrives as a float. Converting back before validating is what
	// stops every stored integer from failing its own type check.
	if d.Kind == KindInt {
		f, ok := v.(float64)
		if !ok {
			return fmt.Errorf("expected a whole number")
		}
		return validate(d, int(f))
	}
	return validate(d, v)
}

func inRange(d Def, n float64) error {
	if d.Min == 0 && d.Max == 0 {
		return nil
	}
	if n < d.Min || n > d.Max {
		return fmt.Errorf("must be between %s and %s, got %s",
			trimNum(d.Min), trimNum(d.Max), trimNum(n))
	}
	return nil
}

func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	}
	return 0, fmt.Errorf("not a number")
}

func trimNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
