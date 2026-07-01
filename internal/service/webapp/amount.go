package webapp

import (
	"fmt"
	"math/big"
	"strings"
)

// parseAmountToI128 converts a human-readable decimal amount string (e.g.
// "123.456") into its raw i128 representation at the given number of
// decimals, returned as (hi, lo) matching xdr.Int128Parts. Only
// non-negative amounts are supported (Stellar balances/transfers are never
// negative).
func parseAmountToI128(amount string, decimals int) (hi int64, lo uint64, err error) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return 0, 0, fmt.Errorf("empty amount")
	}
	neg := strings.HasPrefix(amount, "-")
	if neg {
		return 0, 0, fmt.Errorf("negative amounts are not supported")
	}

	whole, frac, hasFrac := strings.Cut(amount, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > decimals {
		return 0, 0, fmt.Errorf("amount has more than %d decimal places", decimals)
	}
	if hasFrac {
		frac = frac + strings.Repeat("0", decimals-len(frac))
	} else {
		frac = strings.Repeat("0", decimals)
	}

	raw, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok {
		return 0, 0, fmt.Errorf("invalid amount %q", amount)
	}
	if raw.Sign() < 0 || raw.BitLen() > 127 {
		return 0, 0, fmt.Errorf("amount out of i128 range")
	}

	loBig := new(big.Int).And(raw, new(big.Int).SetUint64(^uint64(0)))
	hiBig := new(big.Int).Rsh(raw, 64)
	return hiBig.Int64(), loBig.Uint64(), nil
}

// formatI128Amount converts a raw non-negative i128 (hi, lo) at the given
// number of decimals into a human-readable decimal string, trimming
// trailing fractional zeros (and the decimal point itself if the amount is
// a whole number) to match this backend's other amount-formatting
// conventions.
func formatI128Amount(hi int64, lo uint64, decimals int) string {
	raw := new(big.Int).Lsh(big.NewInt(hi), 64)
	raw.Or(raw, new(big.Int).SetUint64(lo))

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	wholePart, fracPart := new(big.Int).QuoRem(raw, scale, new(big.Int))

	fracStr := fracPart.String()
	if pad := decimals - len(fracStr); pad > 0 {
		fracStr = strings.Repeat("0", pad) + fracStr
	}
	fracStr = strings.TrimRight(fracStr, "0")

	if fracStr == "" {
		return wholePart.String()
	}
	return wholePart.String() + "." + fracStr
}
