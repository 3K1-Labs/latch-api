package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── stub ────────────────────────────────────────────────────────────────────

type stubCosign struct {
	createOut service.CosignRequest
	createErr error
	listOut   []service.CosignRequest
	listErr   error
	getOut    service.CosignRequest
	getErr    error
	addOut    service.CosignRequest
	addErr    error
	submitErr error
	cancelErr error
}

func (s *stubCosign) Create(_ context.Context, _ service.CreateCosignInput) (service.CosignRequest, error) {
	return s.createOut, s.createErr
}
func (s *stubCosign) List(_ context.Context, _ string) ([]service.CosignRequest, error) {
	return s.listOut, s.listErr
}
func (s *stubCosign) Get(_ context.Context, _ string) (service.CosignRequest, error) {
	return s.getOut, s.getErr
}
func (s *stubCosign) AddSignature(_ context.Context, _, _, _ string) (service.CosignRequest, error) {
	return s.addOut, s.addErr
}
func (s *stubCosign) MarkSubmitted(_ context.Context, _, _ string) error { return s.submitErr }
func (s *stubCosign) Cancel(_ context.Context, _ string) error           { return s.cancelErr }

func newCosignHandler(cosign *stubCosign) *CosignHandler {
	return NewCosignHandler(cosign, &stubAudit{}, &stubPushTokens{}, &stubNotifier{})
}

func validCreateBody() *bytes.Reader {
	return postJSONBody(map[string]any{
		"queue_index":     "9f2c1ab34de5",
		"unsigned_tx_xdr": "v1:abc",
		"network":         "testnet",
		"threshold":       2,
	})
}

// ── Create ──────────────────────────────────────────────────────────────────

func TestCosignCreate_InvalidBody(t *testing.T) {
	h := newCosignHandler(&stubCosign{})
	r := gin.New()
	r.POST("/cosign/requests", h.Create)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCosignCreate_MissingThreshold(t *testing.T) {
	h := newCosignHandler(&stubCosign{})
	r := gin.New()
	r.POST("/cosign/requests", h.Create)

	body := postJSONBody(map[string]any{
		"queue_index":     "9f2c1ab34de5",
		"unsigned_tx_xdr": "v1:abc",
		"network":         "testnet",
	})
	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCosignCreate_ServiceError(t *testing.T) {
	h := newCosignHandler(&stubCosign{createErr: errGeneric})
	r := gin.New()
	r.POST("/cosign/requests", h.Create)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests", validCreateBody()), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCosignCreate_Success(t *testing.T) {
	h := newCosignHandler(&stubCosign{createOut: service.CosignRequest{ID: "req-1", Threshold: 2}})
	r := gin.New()
	r.POST("/cosign/requests", h.Create)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests", validCreateBody()), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "req-1", data["id"])
}

// ── List ────────────────────────────────────────────────────────────────────

func TestCosignList_MissingQueueIndex(t *testing.T) {
	h := newCosignHandler(&stubCosign{})
	r := gin.New()
	r.GET("/cosign/requests", h.List)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/cosign/requests", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCosignList_Success(t *testing.T) {
	h := newCosignHandler(&stubCosign{listOut: []service.CosignRequest{{ID: "a"}, {ID: "b"}}})
	r := gin.New()
	r.GET("/cosign/requests", h.List)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/cosign/requests?queue_index=9f2c1ab34de5", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Len(t, data["requests"], 2)
}

// ── Get ─────────────────────────────────────────────────────────────────────

func TestCosignGet_NotFound(t *testing.T) {
	h := newCosignHandler(&stubCosign{getErr: service.ErrCosignNotFound})
	r := gin.New()
	r.GET("/cosign/requests/:id", h.Get)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/cosign/requests/req-1", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCosignGet_Success(t *testing.T) {
	h := newCosignHandler(&stubCosign{getOut: service.CosignRequest{ID: "req-1"}})
	r := gin.New()
	r.GET("/cosign/requests/:id", h.Get)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/cosign/requests/req-1", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── AddSignature ────────────────────────────────────────────────────────────

func TestCosignAddSignature_InvalidBody(t *testing.T) {
	h := newCosignHandler(&stubCosign{})
	r := gin.New()
	r.POST("/cosign/requests/:id/signatures", h.AddSignature)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests/req-1/signatures", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCosignAddSignature_NotPending(t *testing.T) {
	h := newCosignHandler(&stubCosign{addErr: service.ErrCosignNotPending})
	r := gin.New()
	r.POST("/cosign/requests/:id/signatures", h.AddSignature)

	body := postJSONBody(map[string]any{"blind_signer_id": "b1ind", "auth_entry_xdr": "v1:xyz"})
	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests/req-1/signatures", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCosignAddSignature_Success(t *testing.T) {
	h := newCosignHandler(&stubCosign{addOut: service.CosignRequest{ID: "req-1", SignatureCount: 1}})
	r := gin.New()
	r.POST("/cosign/requests/:id/signatures", h.AddSignature)

	body := postJSONBody(map[string]any{"blind_signer_id": "b1ind", "auth_entry_xdr": "v1:xyz"})
	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests/req-1/signatures", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── MarkSubmitted ───────────────────────────────────────────────────────────

func TestCosignMarkSubmitted_InvalidBody(t *testing.T) {
	h := newCosignHandler(&stubCosign{})
	r := gin.New()
	r.POST("/cosign/requests/:id/submission", h.MarkSubmitted)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests/req-1/submission", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCosignMarkSubmitted_Success(t *testing.T) {
	h := newCosignHandler(&stubCosign{})
	r := gin.New()
	r.POST("/cosign/requests/:id/submission", h.MarkSubmitted)

	body := postJSONBody(map[string]any{"tx_hash": "abc123"})
	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests/req-1/submission", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── Cancel ──────────────────────────────────────────────────────────────────

func TestCosignCancel_NotFound(t *testing.T) {
	h := newCosignHandler(&stubCosign{cancelErr: service.ErrCosignNotFound})
	r := gin.New()
	r.DELETE("/cosign/requests/:id", h.Cancel)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodDelete, "/cosign/requests/req-1", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCosignCancel_Success(t *testing.T) {
	h := newCosignHandler(&stubCosign{})
	r := gin.New()
	r.DELETE("/cosign/requests/:id", h.Cancel)

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodDelete, "/cosign/requests/req-1", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── AddSignature push trigger ───────────────────────────────────────────────

func TestCosignAddSignature_NotifiesQueueExcludingActor(t *testing.T) {
	push := &stubPushTokens{tokensOut: []string{"tokA", "tokB"}, done: make(chan struct{})}
	notifier := &stubNotifier{done: make(chan struct{})}
	h := NewCosignHandler(
		&stubCosign{addOut: service.CosignRequest{ID: "req-1", QueueIndex: "qidx-1"}},
		&stubAudit{}, push, notifier,
	)
	r := gin.New()
	r.POST("/cosign/requests/:id/signatures", h.AddSignature)

	body := postJSONBody(map[string]any{"blind_signer_id": "b1ind", "auth_entry_xdr": "v1:xyz"})
	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests/req-1/signatures", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	select {
	case <-notifier.done:
	case <-time.After(2 * time.Second):
		t.Fatal("push notifier was not called")
	}
	assert.Equal(t, "qidx-1", push.gotQueueIndex)
	assert.Equal(t, "b1ind", push.gotExclude) // actor never self-notified
	assert.Equal(t, []string{"tokA", "tokB"}, notifier.gotTokens)
	assert.Equal(t, "qidx-1", notifier.gotQueueIndex)
}

func TestCosignAddSignature_NotifyFailureDoesNotAffectResponse(t *testing.T) {
	push := &stubPushTokens{tokensErr: errGeneric, done: make(chan struct{})}
	h := NewCosignHandler(
		&stubCosign{addOut: service.CosignRequest{ID: "req-1", QueueIndex: "qidx-1"}},
		&stubAudit{}, push, &stubNotifier{},
	)
	r := gin.New()
	r.POST("/cosign/requests/:id/signatures", h.AddSignature)

	body := postJSONBody(map[string]any{"blind_signer_id": "b1ind", "auth_entry_xdr": "v1:xyz"})
	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests/req-1/signatures", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	<-push.done // let the goroutine finish so -race sees the full interleaving
}

func TestCosignAddSignature_NoNotifyOnServiceError(t *testing.T) {
	push := &stubPushTokens{done: make(chan struct{})}
	h := NewCosignHandler(&stubCosign{addErr: service.ErrCosignNotPending}, &stubAudit{}, push, &stubNotifier{})
	r := gin.New()
	r.POST("/cosign/requests/:id/signatures", h.AddSignature)

	body := postJSONBody(map[string]any{"blind_signer_id": "b1ind", "auth_entry_xdr": "v1:xyz"})
	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/cosign/requests/req-1/signatures", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	select {
	case <-push.done:
		t.Fatal("push lookup must not run when AddSignature fails")
	case <-time.After(100 * time.Millisecond):
	}
}
