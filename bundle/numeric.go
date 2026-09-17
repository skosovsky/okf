package bundle

import (
	"context"
	"fmt"
	"math/big"
)

// ParseNonNegativeYAMLUint64 parses the YAML integer lexical forms accepted by
// yaml.v3 and the strict validator: signs, underscores, and 0x/0o/0b prefixes.
// Negative zero is normalized to zero; negative and overflowing values fail.
func ParseNonNegativeYAMLUint64(raw string) (uint64, error) {
	return parseNonNegativeYAMLUint64Context(context.Background(), raw)
}

func parseNonNegativeYAMLUint64Context(ctx context.Context, raw string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	owned, err := stringFromStringContext(ctx, raw)
	if err != nil {
		return 0, err
	}
	// Values exceeding uint64 do not need to reach big.Int. A cancellable scan
	// keeps arbitrary YAML integer spellings bounded before the exact parser.
	digitCount := 0
	for index := 0; index < len(owned); index++ {
		if index%contextStringChunkSize == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		current := owned[index]
		if current >= '0' && current <= '9' || current >= 'a' && current <= 'f' || current >= 'A' && current <= 'F' {
			digitCount++
		}
	}
	if digitCount > 66 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidYAMLUint64, owned)
	}
	var integer big.Int
	if _, ok := integer.SetString(owned, 0); !ok || integer.Sign() < 0 || integer.BitLen() > 64 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidYAMLUint64, owned)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return integer.Uint64(), nil
}
