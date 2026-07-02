package webapp

import (
	"context"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

type CounterService struct {
	soroban         sorobanRPC
	rpcURL          string
	contractAddress string
}

func NewCounterService(soroban sorobanRPC, rpcURL, contractAddress string) *CounterService {
	return &CounterService{soroban: soroban, rpcURL: rpcURL, contractAddress: contractAddress}
}

// GetValue simulates the demo counter contract's get() function. Mirrors
// app/api/counter/route.ts's GET handler: a simulation that doesn't return a
// success result (e.g. an unset counter) reports 0 rather than an error.
func (s *CounterService) GetValue(ctx context.Context) (uint32, error) {
	val, ok, err := simulateReadCall(ctx, s.soroban, s.rpcURL, s.contractAddress, "get")
	if err != nil {
		return 0, fmt.Errorf("simulate counter get: %w", err)
	}
	if !ok || val.Type != xdr.ScValTypeScvU32 || val.U32 == nil {
		return 0, nil
	}
	return uint32(*val.U32), nil
}
