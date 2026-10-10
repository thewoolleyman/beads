package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const ConditionalMetadataMaxBytes = 32768
const ConditionalMetadataRevisionKey = "_bd_conditional_revision"

// StrictJSONObject rejects duplicate members, trailing values and excessive
// nesting. Numbers retain their JSON spelling; there is no float64 conversion.
// Numeric spelling is part of the precondition and operation identity.
func StrictJSONObject(raw []byte) (map[string]any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("JSON must be valid UTF-8")
	}
	if len(raw) == 0 || len(raw) > ConditionalMetadataMaxBytes {
		return nil, fmt.Errorf("JSON object size must be 1..%d bytes", ConditionalMetadataMaxBytes)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	count := 0
	v, err := strictJSONValue(d, 0, &count)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected JSON object")
	}
	return obj, nil
}

func strictJSONValue(d *json.Decoder, depth int, count *int) (any, error) {
	*count++
	if depth > 32 || *count > 10000 {
		return nil, fmt.Errorf("JSON nesting or value count limit exceeded")
	}
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object member")
			}
			if _, exists := m[key]; exists {
				return nil, fmt.Errorf("duplicate JSON member %q", key)
			}
			v, err := strictJSONValue(d, depth+1, count)
			if err != nil {
				return nil, err
			}
			m[key] = v
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("invalid object ending")
		}
		return m, nil
	case '[':
		a := []any{}
		for d.More() {
			v, err := strictJSONValue(d, depth+1, count)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("invalid array ending")
		}
		return a, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}

// DecodeConditionalMetadataRequest validates the whole input before opening a
// write transaction, including unknown keys and all nested duplicate members.
func DecodeConditionalMetadataRequest(raw []byte) (ConditionalMetadataRequest, error) {
	var r ConditionalMetadataRequest
	if _, err := StrictJSONObject(raw); err != nil {
		return r, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	return r, ValidateConditionalMetadataRequest(r)
}

func ValidateConditionalMetadataRequest(r ConditionalMetadataRequest) error {
	if strings.TrimSpace(r.OperationID) == "" || len(r.OperationID) > 255 {
		return fmt.Errorf("operation_id must contain 1..255 bytes")
	}
	expected, err := StrictJSONObject(r.ExpectedMetadata)
	if err != nil {
		return fmt.Errorf("expected_metadata: %w", err)
	}
	desired, err := StrictJSONObject(r.Metadata)
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	old, exists := expected[ConditionalMetadataRevisionKey]
	if exists {
		s, ok := old.(string)
		if !ok || s == "" {
			return fmt.Errorf("invalid prior conditional revision")
		}
	}
	if next, existsNext := desired[ConditionalMetadataRevisionKey]; existsNext && (!exists || next != old) {
		return fmt.Errorf("metadata cannot choose a conditional revision")
	}
	if len(r.Comments) > 32 {
		return fmt.Errorf("at most 32 comments are allowed")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(raw) > ConditionalMetadataMaxBytes {
		return fmt.Errorf("conditional request exceeds %d bytes", ConditionalMetadataMaxBytes)
	}
	return nil
}
