package lite

import (
	"bytes"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Scalars parse directly regardless of format, so reading JSON as a string
// returns its source text. Structured values use their declared format.
// Strict mode stays opt-in so new fields do not break older instances.
func decode[T any](raw, format string, strict bool) (T, error) {
	var v T

	// The type switch compares exact dynamic types, so *time.Duration and
	// *int64 are distinct cases and the order does not matter.
	switch p := any(&v).(type) {
	case *string:
		*p = raw
		return v, nil
	case *[]byte:
		*p = []byte(raw)
		return v, nil
	case *bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return v, scalarErr(raw, "bool")
		}
		*p = b
		return v, nil
	case *time.Duration:
		d, err := time.ParseDuration(raw)
		if err != nil {
			return v, scalarErr(raw, "time.Duration")
		}
		*p = d
		return v, nil
	case *int:
		n, err := strconv.ParseInt(raw, 10, strconv.IntSize)
		if err != nil {
			return v, scalarErr(raw, "int")
		}
		*p = int(n)
		return v, nil
	case *int32:
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return v, scalarErr(raw, "int32")
		}
		*p = int32(n)
		return v, nil
	case *int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return v, scalarErr(raw, "int64")
		}
		*p = n
		return v, nil
	case *uint:
		n, err := strconv.ParseUint(raw, 10, strconv.IntSize)
		if err != nil {
			return v, scalarErr(raw, "uint")
		}
		*p = uint(n)
		return v, nil
	case *uint32:
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return v, scalarErr(raw, "uint32")
		}
		*p = uint32(n)
		return v, nil
	case *uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return v, scalarErr(raw, "uint64")
		}
		*p = n
		return v, nil
	case *float32:
		f, err := strconv.ParseFloat(raw, 32)
		if err != nil {
			return v, scalarErr(raw, "float32")
		}
		*p = float32(f)
		return v, nil
	case *float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return v, scalarErr(raw, "float64")
		}
		*p = f
		return v, nil
	}

	switch format {
	case FormatJSON:
		var opts []jsonv2.Options
		if strict {
			opts = append(opts, jsonv2.RejectUnknownMembers(true))
		}
		if err := jsonv2.Unmarshal([]byte(raw), &v, opts...); err != nil {
			return v, fmt.Errorf("%w: decode json into %T: %w", ErrDecode, v, err)
		}
		return v, nil

	case FormatYAML:
		// yaml.v3 exposes unknown-field rejection only on Decoder.
		if strict {
			if err := strictYAML([]byte(raw), &v); err != nil {
				return v, fmt.Errorf("%w: decode yaml into %T: %w", ErrDecode, v, err)
			}
			return v, nil
		}
		if err := yaml.Unmarshal([]byte(raw), &v); err != nil {
			return v, fmt.Errorf("%w: decode yaml into %T: %w", ErrDecode, v, err)
		}
		return v, nil

	default:
		return v, fmt.Errorf("%w: format=raw only supports scalar types, not %T; "+
			"set the config format to json or yaml for structured decoding", ErrDecode, v)
	}
}

func scalarErr(raw, typ string) error {
	return fmt.Errorf("%w: %q is not a valid %s", ErrDecode, truncate(raw, 64), typ)
}

// truncate cuts on rune boundaries so multi-byte characters stay intact.
func truncate(s string, n int) string {
	for i, cnt := 0, 0; i < len(s); cnt++ {
		if cnt == n {
			return s[:i] + "…"
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s
}

func strictYAML(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(v)
}

// Wire values matching store.Format. Defined separately because the
// client does not import store: these are two ends of one protocol.
const (
	FormatRaw  = "raw"
	FormatJSON = "json"
	FormatYAML = "yaml"
)
