package splitdump

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const defaultStreamSizeMax = int64(1024 * 1024 * 1024)

type SplitDumpOptions struct {
	StreamSizeMax    int64
	StreamSizeMaxSet bool
}

func normalizeSplitDumpOptions(opts SplitDumpOptions) (SplitDumpOptions, error) {
	if opts.StreamSizeMaxSet && opts.StreamSizeMax < 0 {
		return opts, fmt.Errorf("splitdump: stream size max must be >= 0")
	}
	if !opts.StreamSizeMaxSet {
		opts.StreamSizeMax = defaultStreamSizeMax
	}
	return opts, nil
}

func ParseSizeBytes(input string) (int64, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return 0, nil
	}
	if strings.HasPrefix(trimmed, "-") {
		return 0, fmt.Errorf("size must be >= 0")
	}

	idx := 0
	for idx < len(trimmed) {
		c := trimmed[idx]
		if c < '0' || c > '9' {
			break
		}
		idx++
	}
	if idx == 0 {
		return 0, fmt.Errorf("size must start with digits")
	}

	value, err := strconv.ParseInt(trimmed[:idx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size value: %w", err)
	}

	suffix := strings.ToLower(strings.TrimSpace(trimmed[idx:]))
	multiplier := int64(1)
	switch suffix {
	case "", "b":
		multiplier = 1
	case "k", "kb":
		multiplier = 1000
	case "m", "mb":
		multiplier = 1000 * 1000
	case "g", "gb":
		multiplier = 1000 * 1000 * 1000
	case "kib":
		multiplier = 1024
	case "mib":
		multiplier = 1024 * 1024
	case "gib":
		multiplier = 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("invalid size suffix %q", suffix)
	}

	if value > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("size overflows int64")
	}
	return value * multiplier, nil
}
