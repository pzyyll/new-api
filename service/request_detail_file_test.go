// ABOUTME: Unit tests for request-detail file storage keyed by request_id.
// ABOUTME: Covers write/read/clear, path sanitization, and disabled-switch behavior.
package service

import (
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withTempLogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := common.LogDir
	prevEnabled := common.RequestDetailLogEnabled
	common.LogDir = &dir
	common.RequestDetailLogEnabled = true
	t.Cleanup(func() {
		common.LogDir = prev
		common.RequestDetailLogEnabled = prevEnabled
	})
	return dir
}

func TestWriteAndReadRequestDetailBodies(t *testing.T) {
	withTempLogDir(t)

	err := WriteRequestDetailBodies("req123", `{"model":"gpt"}`, "line1\nline2\n")
	require.NoError(t, err)

	got, err := ReadRequestDetailBodies("req123")
	require.NoError(t, err)
	assert.Equal(t, `{"model":"gpt"}`, got.RequestBody)
	assert.Equal(t, "line1\nline2\n", got.ResponseBody)
}

func TestWriteRequestDetailBodies_Disabled(t *testing.T) {
	withTempLogDir(t)
	common.RequestDetailLogEnabled = false

	err := WriteRequestDetailBodies("req123", `{"a":1}`, `{"b":2}`)
	require.NoError(t, err)

	got, err := ReadRequestDetailBodies("req123")
	require.NoError(t, err)
	assert.Equal(t, "", got.RequestBody)
	assert.Equal(t, "", got.ResponseBody)
}

func TestWriteRequestDetailBodies_PartialUpdate(t *testing.T) {
	withTempLogDir(t)

	require.NoError(t, WriteRequestDetailBodies("req123", `{"a":1}`, `{"b":2}`))
	require.NoError(t, WriteRequestDetailBodies("req123", "", `{"b":3}`))

	got, err := ReadRequestDetailBodies("req123")
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, got.RequestBody)
	assert.Equal(t, `{"b":3}`, got.ResponseBody)
}

func TestSanitizeRequestDetailID_RejectsTraversal(t *testing.T) {
	_, err := sanitizeRequestDetailID("../etc/passwd")
	assert.ErrorIs(t, err, errInvalidRequestDetailID)

	_, err = sanitizeRequestDetailID("a/b")
	assert.ErrorIs(t, err, errInvalidRequestDetailID)

	_, err = sanitizeRequestDetailID("")
	assert.ErrorIs(t, err, errInvalidRequestDetailID)
}

func TestClearAllRequestDetailBodies(t *testing.T) {
	root := withTempLogDir(t)
	require.NoError(t, WriteRequestDetailBodies("reqA", `{"a":1}`, ""))
	require.NoError(t, WriteRequestDetailBodies("reqB", "", "resp"))

	removed, err := ClearAllRequestDetailBodies()
	require.NoError(t, err)
	assert.Equal(t, 2, removed)

	got, err := ReadRequestDetailBodies("reqA")
	require.NoError(t, err)
	assert.Equal(t, "", got.RequestBody)

	// Root directory itself remains; children are gone.
	entries, err := filepath.Glob(filepath.Join(root, requestDetailDirName, "*"))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestRequestDetailStorageInfo(t *testing.T) {
	withTempLogDir(t)
	require.NoError(t, WriteRequestDetailBodies("req1", `{"x":1}`, "y"))

	dir, count, size, err := RequestDetailStorageInfo()
	require.NoError(t, err)
	assert.Contains(t, dir, requestDetailDirName)
	assert.Equal(t, 2, count)
	assert.Greater(t, size, int64(0))
}
