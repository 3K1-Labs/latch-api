package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/service"
	"github.com/stretchr/testify/assert"
)

const (
	testMemberBlindID = "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"
	testWalletRef     = "CCQPT7YSBLMLQ2TQXWJTCQTPCDJIGO2XDIE63H6LQJVA4PB7VR4G2222"
)

func newMembershipHandler(m *stubMembership) *MembershipHandler {
	return NewMembershipHandler(m, &stubAudit{})
}

func membershipRouter(h *MembershipHandler) *gin.Engine {
	r := gin.New()
	r.POST("/memberships", h.Announce)
	r.GET("/memberships", h.List)
	return r
}

// ── Announce ──────────────────────────────────────────────────────────────────

func TestMembershipAnnounce_InvalidBody(t *testing.T) {
	r := membershipRouter(newMembershipHandler(&stubMembership{}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodPost, "/memberships", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMembershipAnnounce_ValidationError(t *testing.T) {
	r := membershipRouter(newMembershipHandler(&stubMembership{announceErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"wallet_ref": "bad", "member_blind_ids": []string{"x"}})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/memberships", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMembershipAnnounce_ServiceError(t *testing.T) {
	r := membershipRouter(newMembershipHandler(&stubMembership{announceErr: errGeneric}))

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"wallet_ref": testWalletRef, "member_blind_ids": []string{testMemberBlindID}})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/memberships", body), "uid")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestMembershipAnnounce_Success_BindsAnnouncerFromContext(t *testing.T) {
	m := &stubMembership{}
	r := membershipRouter(newMembershipHandler(m))

	w := httptest.NewRecorder()
	body := postJSONBody(map[string]any{"wallet_ref": testWalletRef, "member_blind_ids": []string{testMemberBlindID}})
	req := withUserID(httptest.NewRequest(http.MethodPost, "/memberships", body), "user-42")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// The announcer principal must come from the auth context, never the body.
	assert.Equal(t, "user-42", m.announcer)
	assert.Equal(t, testWalletRef, m.announcedWalletRef)
	assert.Contains(t, w.Body.String(), "membership announced")
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestMembershipList_ValidationError(t *testing.T) {
	r := membershipRouter(newMembershipHandler(&stubMembership{listErr: service.ErrValidation}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/memberships?member_blind_id=not-hex", nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMembershipList_ServiceError(t *testing.T) {
	r := membershipRouter(newMembershipHandler(&stubMembership{listErr: errGeneric}))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/memberships?member_blind_id="+testMemberBlindID, nil), "uid")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestMembershipList_Success(t *testing.T) {
	m := &stubMembership{listOut: []service.WalletMembership{{WalletRef: testWalletRef}}}
	r := membershipRouter(newMembershipHandler(m))

	w := httptest.NewRecorder()
	req := withUserID(httptest.NewRequest(http.MethodGet, "/memberships?member_blind_id="+testMemberBlindID, nil), "uid")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), testWalletRef)
}
