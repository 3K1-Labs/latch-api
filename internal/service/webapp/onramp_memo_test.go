package webapp

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateUniqueOnRampMemoID(t *testing.T) {
	t.Run("returns a numeric memo id within range on first try", func(t *testing.T) {
		id, err := generateUniqueOnRampMemoID(context.Background(), func(ctx context.Context, memoID string) (bool, error) {
			return false, nil
		})
		require.NoError(t, err)
		n, err := strconv.ParseInt(id, 10, 64)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, int64(onRampMemoMin))
		assert.Less(t, n, int64(onRampMemoMin+onRampMemoSpan))
	})

	t.Run("retries until a candidate is unused", func(t *testing.T) {
		calls := 0
		id, err := generateUniqueOnRampMemoID(context.Background(), func(ctx context.Context, memoID string) (bool, error) {
			calls++
			return calls < 3, nil
		})
		require.NoError(t, err)
		assert.NotEmpty(t, id)
		assert.Equal(t, 3, calls)
	})

	t.Run("gives up after max attempts", func(t *testing.T) {
		_, err := generateUniqueOnRampMemoID(context.Background(), func(ctx context.Context, memoID string) (bool, error) {
			return true, nil
		})
		assert.ErrorIs(t, err, ErrOnRampMemoGenerationFailed)
	})

	t.Run("propagates an exists check error", func(t *testing.T) {
		wantErr := errors.New("db down")
		_, err := generateUniqueOnRampMemoID(context.Background(), func(ctx context.Context, memoID string) (bool, error) {
			return false, wantErr
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, wantErr)
	})
}
