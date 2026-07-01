package webapp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildMultisigAccountInitParams(t *testing.T) {
	signers := []MultisigSignerInit{
		{Type: "delegated", GAddress: randomGAddress(t)},
		{Type: "webauthn", KeyDataHex: testWebauthnKeyDataHex()},
	}
	salt := make([]byte, 32)

	val, err := buildMultisigAccountInitParams(2, salt, signers)
	require.NoError(t, err)
	require.NotNil(t, val.Map)

	entries := **val.Map
	require.Len(t, entries, 3)
	require.Equal(t, "account_salt", string(*entries[0].Key.Sym))
	require.Equal(t, "signers", string(*entries[1].Key.Sym))
	require.Equal(t, "threshold", string(*entries[2].Key.Sym))
	require.Equal(t, uint32(2), uint32(*entries[2].Val.U32))

	signersVec := **entries[1].Val.Vec
	require.Len(t, signersVec, 2)
}

func TestBuildMultisigAccountInitParams_UnsupportedSignerType(t *testing.T) {
	_, err := buildMultisigAccountInitParams(1, make([]byte, 32), []MultisigSignerInit{{Type: "bogus"}})
	require.Error(t, err)
}

func TestRandomSaltHex(t *testing.T) {
	a, err := randomSaltHex()
	require.NoError(t, err)
	require.Len(t, a, 64) // 32 bytes hex-encoded

	b, err := randomSaltHex()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestRandomInviteToken(t *testing.T) {
	a, err := randomInviteToken()
	require.NoError(t, err)
	require.NotEmpty(t, a)

	b, err := randomInviteToken()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}
