package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const handlerCircleUSDCIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

type stubStellarPriceService struct {
	prices map[string]*service.StellarPriceData
	err    error
	assets []service.AssetIdentity
	calls  int
}

func (s *stubStellarPriceService) LookupUSD(_ context.Context, assets []service.AssetIdentity) (map[string]*service.StellarPriceData, error) {
	s.calls++
	s.assets = append([]service.AssetIdentity(nil), assets...)
	if s.prices != nil {
		return s.prices, s.err
	}
	prices := make(map[string]*service.StellarPriceData, len(assets))
	for _, asset := range assets {
		prices[asset.CanonicalID()] = nil
	}
	return prices, s.err
}

func newStellarPricesTestRouter(h *StellarPricesHandler) *gin.Engine {
	router := gin.New()
	router.POST("/v1/prices/lookup", h.LookupPrices)
	return router
}

func performStellarPricesLookup(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/prices/lookup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestStellarPricesLookup_ValidRequest(t *testing.T) {
	canonicalID := "classic:USDC:" + handlerCircleUSDCIssuer
	stub := &stubStellarPriceService{prices: map[string]*service.StellarPriceData{
		"native":    {Price: "0.2", Change24h: nil},
		canonicalID: {Price: "1.0", Change24h: nil},
	}}
	router := newStellarPricesTestRouter(NewStellarPricesHandler(stub))
	body := `{"assets":[{"type":"native"},{"type":"classic","code":"USDC","issuer":"` + strings.ToLower(handlerCircleUSDCIssuer) + `"}]}`

	recorder := performStellarPricesLookup(t, router, body)
	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data     map[string]*service.StellarPriceData `json:"data"`
		Currency string                               `json:"currency"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "usd", response.Currency)
	assert.Contains(t, recorder.Body.String(), `"change_24h":null`)
	assert.Equal(t, "0.2", response.Data["native"].Price)
	assert.Nil(t, response.Data["native"].Change24h)
	assert.Contains(t, response.Data, canonicalID)
	require.Len(t, stub.assets, 2)
	assert.Equal(t, handlerCircleUSDCIssuer, stub.assets[1].Issuer)
}

func TestStellarPricesLookup_UnsupportedIsNullAndProviderErrorStill200(t *testing.T) {
	stub := &stubStellarPriceService{err: errors.New("provider unavailable")}
	router := newStellarPricesTestRouter(NewStellarPricesHandler(stub))
	recorder := performStellarPricesLookup(t, router, `{"assets":[{"type":"native"}]}`)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	data := response["data"].(map[string]any)
	value, present := data["native"]
	assert.True(t, present)
	assert.Nil(t, value)
}

func TestStellarPricesLookup_Validation(t *testing.T) {
	validContract := "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"
	tests := []struct {
		name string
		body string
	}{
		{name: "empty", body: `{"assets":[]}`},
		{name: "unknown kind", body: `{"assets":[{"type":"token"}]}`},
		{name: "malformed G address", body: `{"assets":[{"type":"classic","code":"USDC","issuer":"GNOTVALID"}]}`},
		{name: "malformed C address", body: `{"assets":[{"type":"contract","contract_id":"CNOTVALID"}]}`},
		{name: "invalid cross fields", body: `{"assets":[{"type":"native","contract_id":"` + validContract + `"}]}`},
		{name: "unknown top-level field", body: `{"assets":[{"type":"native"}],"currency":"eur"}`},
		{name: "unknown asset field", body: `{"assets":[{"type":"native","symbol":"XLM"}]}`},
		{name: "trailing JSON", body: `{"assets":[{"type":"native"}]} {}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubStellarPriceService{}
			router := newStellarPricesTestRouter(NewStellarPricesHandler(stub))
			recorder := performStellarPricesLookup(t, router, tc.body)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Equal(t, 0, stub.calls)
		})
	}
}

func TestStellarPricesLookup_Exactly25Entries(t *testing.T) {
	assets := make([]string, 25)
	for i := range assets {
		assets[i] = `{"type":"native"}`
	}
	stub := &stubStellarPriceService{}
	router := newStellarPricesTestRouter(NewStellarPricesHandler(stub))
	recorder := performStellarPricesLookup(t, router, `{"assets":[`+strings.Join(assets, ",")+`]}`)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, 1, stub.calls)
	assert.JSONEq(t, `{"data":{"native":null},"currency":"usd"}`, recorder.Body.String())
}

func TestStellarPricesLookup_PartialResponse(t *testing.T) {
	canonical := "classic:USDC:" + handlerCircleUSDCIssuer
	stub := &stubStellarPriceService{
		prices: map[string]*service.StellarPriceData{"native": {Price: "0.2"}, canonical: nil},
		err:    errors.New("malformed upstream USDC price"),
	}
	router := newStellarPricesTestRouter(NewStellarPricesHandler(stub))
	recorder := performStellarPricesLookup(t, router, `{"assets":[{"type":"native"},{"type":"classic","code":"USDC","issuer":"`+handlerCircleUSDCIssuer+`"}]}`)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"data":{"native":{"price":"0.2","change_24h":null},"`+canonical+`":null},"currency":"usd"}`, recorder.Body.String())
}

func TestStellarPricesLookup_LimitAppliesBeforeDeduplication(t *testing.T) {
	assets := make([]string, 26)
	for i := range assets {
		assets[i] = `{"type":"native"}`
	}
	stub := &stubStellarPriceService{}
	router := newStellarPricesTestRouter(NewStellarPricesHandler(stub))
	recorder := performStellarPricesLookup(t, router, `{"assets":[`+strings.Join(assets, ",")+`]}`)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Equal(t, 0, stub.calls)
}
