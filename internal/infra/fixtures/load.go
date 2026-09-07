package fixtures

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Load reads a scenario from a YAML file.
func Load(path string) (*Scenario, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open fixture %s: %w", path, err)
	}

	defer func() { _ = f.Close() }()

	s, err := Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to load fixture %s: %w", path, err)
	}

	return s, nil
}

// Decode reads a scenario from any source, so tests can supply a literal.
func Decode(r io.Reader) (*Scenario, error) {
	var s Scenario

	dec := yaml.NewDecoder(r)
	// Unknown keys are a typo in a fixture, not something to serve silently.
	dec.KnownFields(true)

	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("failed to decode scenario: %w", err)
	}

	if err := s.Validate(); err != nil {
		return nil, err
	}

	return &s, nil
}

// LoadDir reads every .yaml file in a directory, keyed by scenario name. The
// mock serves one at a time; the control plane switches between them.
func LoadDir(dir string) (map[string]*Scenario, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("failed to scan fixture directory %s: %w", dir, err)
	}

	out := make(map[string]*Scenario, len(entries))

	for _, path := range entries {
		s, loadErr := Load(path)
		if loadErr != nil {
			return nil, loadErr
		}

		if _, clash := out[s.Name]; clash {
			return nil, fmt.Errorf("two scenarios are both named %q", s.Name)
		}

		out[s.Name] = s
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no scenarios found in %s", dir)
	}

	return out, nil
}

// Validate catches the cross-references a scenario can get wrong, so a typo
// surfaces at boot rather than as a half-populated incident at request time.
func (s *Scenario) Validate() error {
	if s.Name == "" {
		return errors.New("scenario is missing a name")
	}

	refs := s.index()

	for _, e := range s.Catalog.Entries {
		if err := refs.known("catalog type", refs.types, e.Type, "catalog entry "+e.Name); err != nil {
			return err
		}
	}

	for _, in := range s.Incidents {
		if err := refs.validateIncident(in); err != nil {
			return err
		}
	}

	return nil
}

// references is the set of identifiers a scenario declares, used to check that
// everything pointing at them actually resolves.
type references struct {
	severities map[string]bool
	statuses   map[string]bool
	roles      map[string]bool
	users      map[string]bool
	alerts     map[string]bool
	types      map[string]bool
}

func (s *Scenario) index() references {
	return references{
		severities: indexBy(s.Severities, func(x Severity) string { return x.ID }),
		statuses:   indexBy(s.Statuses, func(x Status) string { return x.ID }),
		roles:      indexBy(s.Roles, func(x Role) string { return x.ID }),
		users:      indexBy(s.Users, func(x User) string { return x.ID }),
		alerts:     indexBy(s.Alerts, func(x Alert) string { return x.ID }),
		types:      indexBy(s.Catalog.Types, func(x CatalogType) string { return x.ID }),
	}
}

// known reports an error when ref is set but names nothing declared. An empty
// ref is always fine: fixtures only spell out what they care about.
func (r references) known(kind string, ids map[string]bool, ref, owner string) error {
	if ref == "" || ids[ref] {
		return nil
	}

	return fmt.Errorf("%s references unknown %s %q", owner, kind, ref)
}

func (r references) validateIncident(in Incident) error {
	owner := "incident " + in.ID

	if err := r.known("status", r.statuses, in.Status, owner); err != nil {
		return err
	}

	if err := r.known("severity", r.severities, in.Severity, owner); err != nil {
		return err
	}

	for roleID, userID := range in.Roles {
		if err := r.known("role", r.roles, roleID, owner); err != nil {
			return err
		}

		if err := r.known("user", r.users, userID, owner); err != nil {
			return err
		}
	}

	for _, alertID := range in.Alerts {
		if err := r.known("alert", r.alerts, alertID, owner); err != nil {
			return err
		}
	}

	for _, u := range in.Updates {
		if err := r.validateUpdate(u, owner+" update"); err != nil {
			return err
		}
	}

	for _, t := range in.Timeline {
		if err := r.known("user", r.users, t.Creator, owner+" timeline item"); err != nil {
			return err
		}
	}

	return nil
}

func (r references) validateUpdate(u Update, owner string) error {
	if err := r.known("status", r.statuses, u.Status, owner); err != nil {
		return err
	}

	if err := r.known("severity", r.severities, u.Severity, owner); err != nil {
		return err
	}

	return r.known("user", r.users, u.Updater, owner)
}

func indexBy[T any](items []T, key func(T) string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[key(item)] = true
	}

	return out
}

// LoadedAt is the clock a scenario's relative timestamps resolve against.
// Tests override it to make expansion deterministic.
type LoadedAt func() time.Time
