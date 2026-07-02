package webapp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
)

const (
	onRampMemoMin         = 1_000_000_000
	onRampMemoSpan        = 281_474_976_710_655 // matches crypto.randomInt's max span (2^48 - 1)
	onRampMemoMaxAttempts = 12
)

var ErrOnRampMemoGenerationFailed = errors.New("failed to generate a unique on-ramp memo id")

// generateUniqueOnRampMemoID mirrors lib/on-ramp/memo.ts's generateUniqueMemoId:
// draw a random 10-digit memo candidate and retry (up to onRampMemoMaxAttempts)
// until exists reports it unused.
func generateUniqueOnRampMemoID(ctx context.Context, exists func(ctx context.Context, memoID string) (bool, error)) (string, error) {
	span := big.NewInt(onRampMemoSpan)
	for attempt := 0; attempt < onRampMemoMaxAttempts; attempt++ {
		n, err := rand.Int(rand.Reader, span)
		if err != nil {
			return "", fmt.Errorf("read random memo offset: %w", err)
		}
		memoID := strconv.FormatInt(onRampMemoMin+n.Int64(), 10)

		used, err := exists(ctx, memoID)
		if err != nil {
			return "", fmt.Errorf("check on-ramp memo id existence: %w", err)
		}
		if !used {
			return memoID, nil
		}
	}
	return "", ErrOnRampMemoGenerationFailed
}
