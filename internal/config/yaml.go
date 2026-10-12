package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// decodeStrict decodes exactly one YAML document into v, rejecting unknown
// fields so that typos and unsupported options fail instead of being ignored.
func decodeStrict(data []byte, v any) error {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("file is empty")
		}
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("file must contain exactly one YAML document")
	}
	return nil
}
