package webapp

import (
	"context"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCounterService(t *testing.T, responses ...*xdr.ScVal) *CounterService {
	t.Helper()
	rpc := &simulateReadFakeRPC{t: t, responses: responses}
	return NewCounterService(rpc, "https://rpc.example.com", testContractAddress(t))
}

func TestCounterService_GetValue(t *testing.T) {
	t.Run("returns the simulated u32 value", func(t *testing.T) {
		val7 := scU32(7)
		svc := newCounterService(t, &val7)
		val, err := svc.GetValue(context.Background())
		require.NoError(t, err)
		assert.Equal(t, uint32(7), val)
	})

	t.Run("reports 0 when simulation is not a success", func(t *testing.T) {
		svc := newCounterService(t, nil)
		val, err := svc.GetValue(context.Background())
		require.NoError(t, err)
		assert.Equal(t, uint32(0), val)
	})

	t.Run("reports 0 for an unexpected return type", func(t *testing.T) {
		str := scString("not-a-number")
		svc := newCounterService(t, &str)
		val, err := svc.GetValue(context.Background())
		require.NoError(t, err)
		assert.Equal(t, uint32(0), val)
	})
}
