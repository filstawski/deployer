package config

import (
	"fmt"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that YAML files write as "30m", "4h", or "7d".
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("line %d: expected a duration such as 30m, 4h, or 7d", node.Line)
	}
	v, err := domain.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implements yaml.Marshaler using the largest exact unit.
func (d Duration) MarshalYAML() (any, error) {
	v := time.Duration(d)
	switch {
	case v != 0 && v%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", v/(24*time.Hour)), nil
	case v != 0 && v%time.Hour == 0:
		return fmt.Sprintf("%dh", v/time.Hour), nil
	case v != 0 && v%time.Minute == 0:
		return fmt.Sprintf("%dm", v/time.Minute), nil
	default:
		return v.String(), nil
	}
}
