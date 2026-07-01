package webapp

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBundlerService_ValidSecret(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)

	svc, err := NewBundlerService(kp.Seed(), "")
	require.NoError(t, err)
	assert.Equal(t, kp.Address(), svc.PublicKey())
	assert.Equal(t, kp.Address(), svc.Keypair().Address())
}

func TestNewBundlerService_EmptySecret(t *testing.T) {
	_, err := NewBundlerService("", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BUNDLER_SECRET")
}

func TestNewBundlerService_InvalidSecret(t *testing.T) {
	_, err := NewBundlerService("not-a-valid-seed", "")
	require.Error(t, err)
}

func TestNewBundlerService_ValidLegacySecret(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)
	legacyKP, err := keypair.Random()
	require.NoError(t, err)

	svc, err := NewBundlerService(kp.Seed(), legacyKP.Seed())
	require.NoError(t, err)

	found, ok := svc.ResolveSignerKeypairForG(legacyKP.Address())
	require.True(t, ok)
	assert.Equal(t, legacyKP.Address(), found.Address())
}

func TestNewBundlerService_InvalidLegacySecretIsNonFatal(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)

	svc, err := NewBundlerService(kp.Seed(), "not-a-valid-seed")
	require.NoError(t, err)
	assert.NotNil(t, svc)

	_, ok := svc.ResolveSignerKeypairForG("GSOMEOTHERADDRESS")
	assert.False(t, ok)
}

func TestResolveSignerKeypairForG_MatchesBundler(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)
	svc, err := NewBundlerService(kp.Seed(), "")
	require.NoError(t, err)

	found, ok := svc.ResolveSignerKeypairForG(kp.Address())
	require.True(t, ok)
	assert.Equal(t, kp.Address(), found.Address())
}

func TestResolveSignerKeypairForG_NoMatch(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)
	svc, err := NewBundlerService(kp.Seed(), "")
	require.NoError(t, err)

	other, err := keypair.Random()
	require.NoError(t, err)

	_, ok := svc.ResolveSignerKeypairForG(other.Address())
	assert.False(t, ok)
}
