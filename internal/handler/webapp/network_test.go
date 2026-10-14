package webapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/deploynet"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFailNetworkResolution_Mismatch(t *testing.T) {
	deploynet.SetForTest(t, deploynet.Mainnet)
	_, err := webapp.ParseNetwork("testnet")
	require.ErrorIs(t, err, deploynet.ErrMismatch)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	failNetworkResolution(c, err)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "network_mismatch", resp["code"])
}

func TestParseNetwork_DeploymentNetwork(t *testing.T) {
	deploynet.SetForTest(t, deploynet.Mainnet)
	got, err := webapp.ParseNetwork("")
	require.NoError(t, err)
	assert.Equal(t, webapp.NetworkMainnet, got)

	_, err = webapp.ParseNetwork("futurenet")
	assert.ErrorIs(t, err, webapp.ErrInvalidNetwork)
}
