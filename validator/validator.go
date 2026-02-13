package validator

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

var (
	schemaOnce   sync.Once
	schemaCached *jsonschema.Schema
	schemaErr    error
)

func Load(fs embed.FS, config map[string]string) (map[string]any, error) {
	s, err := compiledSchema(fs)
	if err != nil {
		return nil, fmt.Errorf("load embedded schema: %w", err)
	}
	tc, err := typedConfig(config)
	if err != nil {
		return nil, err
	}
	if err := s.Validate(tc); err != nil {
		return nil, formatSchemaErr(err)
	}
	return tc, nil
}

func compiledSchema(fs embed.FS) (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		b, err := fs.ReadFile("schema.json")
		if err != nil {
			schemaErr = err
			return
		}

		c := jsonschema.NewCompiler()
		// AddResource wants an io.Reader
		if err := c.AddResource("mem://schema.json", bytes.NewReader(b)); err != nil {
			schemaErr = err
			return
		}

		schemaCached, schemaErr = c.Compile("mem://schema.json")
	})
	return schemaCached, schemaErr
}

func typedConfig(config map[string]string) (map[string]any, error) {
	out := make(map[string]any, len(config))
	for k, v := range config {
		switch k {
		case "use_tls", "tls_insecure_no_verify":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("invalid %s value", k)
			}
			out[k] = b
		default:
			out[k] = v
		}
	}
	return out, nil
}

func formatSchemaErr(err error) error {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err
	}

	// Collect leaf causes (the “real” problems).
	leaves := collectLeaves(ve)

	// Turn each leaf into a human-ish line.
	lines := make([]string, 0, len(leaves))
	for _, l := range leaves {
		lines = append(lines, formatLeaf(l))
	}

	// stable output
	sort.Strings(lines)

	// If there’s only one line, keep it one-liner.
	if len(lines) == 1 {
		return fmt.Errorf("invalid config: %s", lines[0])
	}

	return fmt.Errorf("invalid config:\n  - %s", strings.Join(lines, "\n  - "))
}

func collectLeaves(ve *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return []*jsonschema.ValidationError{ve}
	}
	var out []*jsonschema.ValidationError
	for _, c := range ve.Causes {
		out = append(out, collectLeaves(c)...)
	}
	return out
}

func formatLeaf(ve *jsonschema.ValidationError) string {
	// InstanceLocation is a JSON Pointer like "/access_key"
	loc := strings.TrimPrefix(ve.InstanceLocation, "/")
	loc = strings.ReplaceAll(loc, "/", ".")
	if loc == "" {
		loc = "(root)"
	}

	// Special-case required errors to make them nicer.
	// ve.Message often looks like: "missing properties: 'access_key'"
	msg := ve.Message
	msg = strings.ReplaceAll(msg, "missing properties:", "missing required:")
	msg = strings.ReplaceAll(msg, "'", "")

	if loc == "(root)" {
		return msg
	}
	return fmt.Sprintf("%s: %s", loc, msg)
}
