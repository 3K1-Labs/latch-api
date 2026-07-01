package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service/webapp"
	"github.com/stretchr/testify/assert"
)

// ── BuildSend ────────────────────────────────────────────────────────────────

func TestBuildSend_Success(t *testing.T) {
	stub := &stubTransaction{buildSendResult: webapp.BuildSendResult{
		BuildAuthTransactionResult: webapp.BuildAuthTransactionResult{
			TxXdr:         "AAAAtx==",
			AuthEntryXdr:  "AAAAauth==",
			ContextRuleID: 1,
			SubmitMethod:  "webauthn",
		},
		Asset:     webapp.CatalogAsset{AssetID: "native", Symbol: "XLM", ContractID: "CCONTRACT", Decimals: 7},
		Recipient: "GRECIPIENT",
		Amount:    "1.5",
		AmountRaw: "15000000",
	}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"txXdr":"AAAAtx=="`)
	assert.Contains(t, w.Body.String(), `"submitMethod":"webauthn"`)
}

func TestBuildSend_NumericAmount(t *testing.T) {
	stub := &stubTransaction{buildSendResult: webapp.BuildSendResult{}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              1.5,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBuildSend_InvalidBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildSend_FreighterMissingSignerG(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "freighter",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildSend_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{buildSendErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildSend_BadAssetAllowlistJSON(t *testing.T) {
	cfg := testCfg()
	cfg.WebAppAssetAllowlistJSON = "{not json"
	h := NewTransactionHandler(&stubTransaction{}, cfg)
	r := gin.New()
	r.POST("/transaction/build-send", h.BuildSend)

	req := httptest.NewRequest(http.MethodPost, "/transaction/build-send", postJSONBody(map[string]any{
		"smartAccountAddress": "CADDRESS",
		"signerType":          "webauthn",
		"assetId":             "native",
		"recipient":           "GRECIPIENT",
		"amount":              "1.5",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── SubmitWebAuthn ───────────────────────────────────────────────────────────

func TestSubmitWebAuthn_Success(t *testing.T) {
	stub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "deadbeef", Status: "SUCCESS"}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr":         "AAAAtx==",
		"authEntryXdr":  "AAAAauth==",
		"sigDataXdr":    "AAAAsig==",
		"keyDataHex":    "aabbcc",
		"contextRuleId": 1,
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"deadbeef"`)
	assert.Contains(t, w.Body.String(), `"status":"SUCCESS"`)
}

func TestSubmitWebAuthn_InvalidBody(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr": "AAAAtx==",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitWebAuthn_MissingAuthEntry(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr":      "AAAAtx==",
		"sigDataXdr": "AAAAsig==",
		"keyDataHex": "aabbcc",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitWebAuthn_AuthEntriesXdrAccepted(t *testing.T) {
	stub := &stubTransaction{submitResult: webapp.SubmitResult{Hash: "deadbeef", Status: "SUCCESS"}}
	h := NewTransactionHandler(stub, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr":          "AAAAtx==",
		"authEntriesXdr": []string{"AAAAauth1==", "AAAAauth2=="},
		"sigDataXdr":     "AAAAsig==",
		"keyDataHex":     "aabbcc",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSubmitWebAuthn_ServiceError(t *testing.T) {
	h := NewTransactionHandler(&stubTransaction{submitErr: assertErr}, testCfg())
	r := gin.New()
	r.POST("/transaction/submit-webauthn", h.SubmitWebAuthn)

	req := httptest.NewRequest(http.MethodPost, "/transaction/submit-webauthn", postJSONBody(map[string]any{
		"txXdr":        "AAAAtx==",
		"authEntryXdr": "AAAAauth==",
		"sigDataXdr":   "AAAAsig==",
		"keyDataHex":   "aabbcc",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
