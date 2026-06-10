package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validRID = "22222222-2222-2222-2222-222222222222"

func TestNewCosignService(t *testing.T) {
	assert.NotNil(t, NewCosignService(nil))
}

// ── Create ──────────────────────────────────────────────────────────────────

func TestCosignServiceCreate_InsertError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.Create(context.Background(), CreateCosignInput{
		QueueIndex: "qidx", UnsignedTxXDR: "v1:x", Network: "testnet", Threshold: 2,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert cosign request")
}

// ── List ────────────────────────────────────────────────────────────────────

func TestCosignServiceList_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.List(context.Background(), "qidx")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list cosign requests")
}

// ── Get / getByID ─────────────────────────────────────────────────────────────

func TestCosignServiceGet_MalformedID(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.Get(context.Background(), "not-a-uuid")
	require.ErrorIs(t, err, ErrCosignNotFound)
}

func TestCosignServiceGet_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.Get(context.Background(), validRID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get cosign request")
}

// ── AddSignature ──────────────────────────────────────────────────────────────

func TestCosignServiceAddSignature_MalformedID(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.AddSignature(context.Background(), "not-a-uuid", "blind", "xdr")
	require.ErrorIs(t, err, ErrCosignNotFound)
}

func TestCosignServiceAddSignature_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.AddSignature(context.Background(), validRID, "blind", "xdr")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get cosign request")
}

// ── MarkSubmitted / Cancel ────────────────────────────────────────────────────

func TestCosignServiceMarkSubmitted_MalformedID(t *testing.T) {
	svc := NewCosignService(errorQueries())
	err := svc.MarkSubmitted(context.Background(), "not-a-uuid", "hash")
	require.ErrorIs(t, err, ErrCosignNotFound)
}

func TestCosignServiceMarkSubmitted_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	err := svc.MarkSubmitted(context.Background(), validRID, "hash")
	require.Error(t, err)
}

func TestCosignServiceCancel_MalformedID(t *testing.T) {
	svc := NewCosignService(errorQueries())
	err := svc.Cancel(context.Background(), "not-a-uuid")
	require.ErrorIs(t, err, ErrCosignNotFound)
}

func TestCosignServiceCancel_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	err := svc.Cancel(context.Background(), validRID)
	require.Error(t, err)
}
