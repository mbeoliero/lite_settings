package store

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"regexp"

	"gopkg.in/yaml.v3"
)

// MaxValueSize caps values to protect full-prefix reads from large payloads.
const MaxValueSize = 1 << 20 // 1 MiB

// MaxKeyLen matches the config_key column width.
const MaxKeyLen = 191

// Audit limits match column widths and prevent database errors or truncation.
const (
	MaxAuthorLen  = 64
	MaxCommentLen = 255
)

// keyPattern excludes backslash for complete LIKE escaping, slash for direct
// URL path embedding, and ambiguous whitespace or control characters.
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:@-]+$`)

// ValidateKey checks a key's character set and length.
func ValidateKey(key string) error {
	switch {
	case key == "":
		return fmt.Errorf("%w: empty", ErrInvalidKey)
	case len(key) > MaxKeyLen:
		return fmt.Errorf("%w: %d bytes exceeds limit %d", ErrInvalidKey, len(key), MaxKeyLen)
	case !keyPattern.MatchString(key):
		return fmt.Errorf("%w: %q contains characters outside [A-Za-z0-9_.:@-]", ErrInvalidKey, key)
	}
	return nil
}

// ValidateChange bounds audit fields while allowing human free text.
func ValidateChange(c Change) error {
	if len(c.Author) > MaxAuthorLen {
		return fmt.Errorf("%w: author %d bytes exceeds limit %d", ErrInvalidChange, len(c.Author), MaxAuthorLen)
	}
	if len(c.Comment) > MaxCommentLen {
		return fmt.Errorf("%w: comment %d bytes exceeds limit %d", ErrInvalidChange, len(c.Comment), MaxCommentLen)
	}
	return nil
}

// ValidateValue checks size and format syntax, not application schema.
func ValidateValue(value string, format Format) error {
	if len(value) > MaxValueSize {
		return fmt.Errorf("%w: %d bytes exceeds limit %d", ErrInvalidValue, len(value), MaxValueSize)
	}

	switch format {
	case FormatRaw:
		return nil
	case FormatJSON:
		if err := validateJSON([]byte(value)); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidValue, err)
		}
		return nil
	case FormatYAML:
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(value), &node); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidValue, err)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown format %q", ErrInvalidValue, format)
	}
}

// validateJSON uses decoder tokens to preserve useful error locations.
func validateJSON(data []byte) error {
	dec := jsontext.NewDecoder(bytes.NewReader(data))

	if _, err := dec.ReadValue(); err != nil {

		if errors.Is(err, io.EOF) {
			return errors.New("empty JSON document")
		}
		return err
	}

	// Reject multiple documents; the decoder otherwise accepts a JSON stream.
	_, err := dec.ReadToken()
	if err == nil {
		return errors.New("unexpected trailing data after top-level JSON value")
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
