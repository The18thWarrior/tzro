package verification

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Preset defines the repository verification configuration.
type Preset struct {
	Version            int           `yaml:"version"`
	Timeout            time.Duration `yaml:"-"`
	TimeoutStr         string        `yaml:"timeout"`
	SummaryTargetBytes int           `yaml:"summary_target_bytes"`
	Checks             []PresetCheck `yaml:"checks"`
}

// PresetCheck defines a single required check command.
type PresetCheck struct {
	ID            string   `yaml:"id"`
	Argv          []string `yaml:"argv"`
	Cwd           string   `yaml:"cwd"`
	ParallelGroup string   `yaml:"parallel_group,omitempty"`
	DependsOn     []string `yaml:"depends_on,omitempty"`
}

// LoadPreset reads and validates a .tzro/verification.yaml file.
func LoadPreset(path string) (*Preset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("verification preset not found: %s", path)
		}
		return nil, fmt.Errorf("cannot read preset: %w", err)
	}

	var p Preset
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid preset YAML: %w", err)
	}

	if p.Version != 1 {
		return nil, fmt.Errorf("unsupported preset version: %d", p.Version)
	}

	// Parse timeout duration.
	if p.TimeoutStr != "" {
		d, err := time.ParseDuration(p.TimeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout %q: %w", p.TimeoutStr, err)
		}
		p.Timeout = d
	}

	// Validate checks.
	ids := map[string]bool{}
	for i, c := range p.Checks {
		if c.ID == "" {
			return nil, fmt.Errorf("check %d: id is required", i)
		}
		if ids[c.ID] {
			return nil, fmt.Errorf("check %d: duplicate id %q", i, c.ID)
		}
		ids[c.ID] = true

		if len(c.Argv) == 0 {
			return nil, fmt.Errorf("check %q: argv is required", c.ID)
		}

		// Validate dependency references.
		for _, dep := range c.DependsOn {
			if !ids[dep] {
				found := false
				for _, other := range p.Checks {
					if other.ID == dep {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("check %q: depends_on references unknown check %q", c.ID, dep)
				}
			}
		}
	}

	// Detect dependency cycles.
	if err := detectCycles(p.Checks); err != nil {
		return nil, err
	}

	return &p, nil
}

// detectCycles uses topological sort to find dependency cycles.
func detectCycles(checks []PresetCheck) error {
	type state int
	const (
		unvisited state = iota
		visiting
		visited
	)
	states := make(map[string]state)
	deps := make(map[string][]string)
	for _, c := range checks {
		states[c.ID] = unvisited
		deps[c.ID] = c.DependsOn
	}

	var visit func(id string) error
	visit = func(id string) error {
		switch states[id] {
		case visiting:
			return fmt.Errorf("dependency cycle involving check %q", id)
		case visited:
			return nil
		}
		states[id] = visiting
		for _, dep := range deps[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		states[id] = visited
		return nil
	}

	for _, c := range checks {
		if err := visit(c.ID); err != nil {
			return err
		}
	}
	return nil
}
