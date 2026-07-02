package webapp

import (
	"context"
	"testing"

	"github.com/latch/backend/internal/service"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildTestRuleScVal(name string, isDefault bool, callContractAddr string, signers ...xdr.ScVal) xdr.ScVal {
	var contextType xdr.ScVal
	if isDefault {
		contextType = scSymbol("Default")
	} else {
		addrVal, _ := scAddress(callContractAddr)
		contextType = scVec(scSymbol("CallContract"), addrVal)
	}
	return scMap(
		scMapEntry("context_type", contextType),
		scMapEntry("name", scString(name)),
		scMapEntry("signers", scVec(signers...)),
	)
}

func externalSignerScVal(t *testing.T, verifierAddr string, keyData []byte) xdr.ScVal {
	t.Helper()
	verifierVal, err := scAddress(verifierAddr)
	require.NoError(t, err)
	return scVec(scSymbol("External"), verifierVal, scBytes(keyData))
}

func delegatedSignerScVal(t *testing.T, gAddress string) xdr.ScVal {
	t.Helper()
	gVal, err := scAddress(gAddress)
	require.NoError(t, err)
	return scVec(scSymbol("Delegated"), gVal)
}

// simulateReadFakeRPC serves canned simulateReadCall responses keyed by call
// order, for testing ContextRulesService without a live Soroban RPC. A nil
// entry in responses means "not found" (simulateReadCall's ok=false).
type simulateReadFakeRPC struct {
	fakeSorobanRPC
	t         *testing.T
	responses []*xdr.ScVal
	call      int
}

func (f *simulateReadFakeRPC) SimulateTransaction(ctx context.Context, rpcURL, txXDR string, rc service.RPCResourceConfig) (*service.SimulateResult, error) {
	f.t.Helper()
	require.Less(f.t, f.call, len(f.responses), "unexpected extra simulateReadCall")
	val := f.responses[f.call]
	f.call++
	if val == nil {
		return &service.SimulateResult{}, nil
	}
	b64, err := xdr.MarshalBase64(*val)
	require.NoError(f.t, err)
	return &service.SimulateResult{Results: []service.SimResultEntry{{XDR: b64}}}, nil
}

func newContextRulesService(t *testing.T, responses ...xdr.ScVal) *ContextRulesService {
	t.Helper()
	ptrs := make([]*xdr.ScVal, len(responses))
	for i := range responses {
		ptrs[i] = &responses[i]
	}
	rpc := &simulateReadFakeRPC{t: t, responses: ptrs}
	return NewContextRulesService(rpc, "https://rpc.example.com")
}

func TestDiscoverContextRule_Matched(t *testing.T) {
	targetContract := testContractAddress(t)
	svc := newContextRulesService(t,
		scU32(2), // count
		buildTestRuleScVal("default", true, ""),
		buildTestRuleScVal("send-usdc", false, targetContract),
	)

	id, discovery, err := svc.DiscoverContextRule(context.Background(), testContractAddress(t), targetContract)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), id)
	assert.Equal(t, ContextRuleDiscoveryMatched, discovery)
}

func TestDiscoverContextRule_FallsBackToDefault(t *testing.T) {
	targetContract := testContractAddress(t)
	svc := newContextRulesService(t,
		scU32(1),
		buildTestRuleScVal("default", true, ""),
	)

	id, discovery, err := svc.DiscoverContextRule(context.Background(), testContractAddress(t), targetContract)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), id)
	assert.Equal(t, ContextRuleDiscoveryDefault, discovery)
}

func TestDiscoverContextRule_FallbackWhenNoRules(t *testing.T) {
	svc := newContextRulesService(t, scU32(0))

	id, discovery, err := svc.DiscoverContextRule(context.Background(), testContractAddress(t), testContractAddress(t))
	require.NoError(t, err)
	assert.Equal(t, uint32(0), id)
	assert.Equal(t, ContextRuleDiscoveryFallback, discovery)
}

func TestDiscoverDefaultContextRule_Found(t *testing.T) {
	svc := newContextRulesService(t,
		scU32(2),
		buildTestRuleScVal("send-usdc", false, testContractAddress(t)),
		buildTestRuleScVal("default", true, ""),
	)

	id, discovery, err := svc.DiscoverDefaultContextRule(context.Background(), testContractAddress(t))
	require.NoError(t, err)
	assert.Equal(t, uint32(1), id)
	assert.Equal(t, ContextRuleDiscoveryDefault, discovery)
}

func TestListContextRules_ParsesSignersAndFields(t *testing.T) {
	verifier := testGAddress
	svc := newContextRulesService(t,
		scU32(1),
		buildTestRuleScVal("send-usdc", false, testContractAddress(t),
			externalSignerScVal(t, verifier, []byte{0x01, 0x02}),
			delegatedSignerScVal(t, testGAddress),
		),
	)

	rules, err := svc.ListContextRules(context.Background(), testContractAddress(t))
	require.NoError(t, err)
	require.Len(t, rules, 1)

	rule := rules[0]
	assert.Equal(t, uint32(0), rule.ID)
	assert.Equal(t, "send-usdc", rule.Name)
	assert.False(t, rule.IsDefault)
	require.Len(t, rule.Signers, 2)
	assert.Equal(t, "External", rule.Signers[0].Kind)
	assert.Equal(t, verifier, rule.Signers[0].VerifierAddress)
	assert.Equal(t, "0102", rule.Signers[0].KeyDataHex)
	assert.Equal(t, "Delegated", rule.Signers[1].Kind)
	assert.Equal(t, testGAddress, rule.Signers[1].GAddress)
}

func TestListContextRules_NoRules(t *testing.T) {
	svc := newContextRulesService(t, scU32(0))
	rules, err := svc.ListContextRules(context.Background(), testContractAddress(t))
	require.NoError(t, err)
	assert.Empty(t, rules)
}
