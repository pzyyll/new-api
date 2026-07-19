// ABOUTME: Persist request/response detail bodies as files keyed by request_id.
// ABOUTME: Used for temporary debug logging without storing large bodies in the DB.
package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/gin-gonic/gin"
)

const (
	requestDetailDirName      = "request-detail"
	requestDetailRequestFile  = "request.json"
	requestDetailResponseFile = "response.txt"
	maxRequestDetailIDLength  = 128
)

var (
	errInvalidRequestDetailID = errors.New("invalid request id")
	errRequestDetailDisabled  = errors.New("request detail log directory unavailable")
)

// RequestDetailBodies is the on-disk payload for one request_id.
type RequestDetailBodies struct {
	RequestBody  string `json:"request_body,omitempty"`
	ResponseBody string `json:"response_body,omitempty"`
}

func requestDetailRoot() (string, error) {
	if common.LogDir == nil || strings.TrimSpace(*common.LogDir) == "" {
		return "", errRequestDetailDisabled
	}
	return filepath.Join(*common.LogDir, requestDetailDirName), nil
}

func sanitizeRequestDetailID(requestID string) (string, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || len(requestID) > maxRequestDetailIDLength {
		return "", errInvalidRequestDetailID
	}
	for _, r := range requestID {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", errInvalidRequestDetailID
	}
	if requestID == "." || requestID == ".." {
		return "", errInvalidRequestDetailID
	}
	return requestID, nil
}

func requestDetailDir(requestID string) (string, error) {
	root, err := requestDetailRoot()
	if err != nil {
		return "", err
	}
	safeID, err := sanitizeRequestDetailID(requestID)
	if err != nil {
		return "", err
	}
	// Ensure the resolved path stays under the root (defense in depth).
	dir := filepath.Join(root, safeID)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	sep := string(os.PathSeparator)
	if absDir != absRoot && !strings.HasPrefix(absDir, absRoot+sep) {
		return "", errInvalidRequestDetailID
	}
	return absDir, nil
}

// PersistRequestDetailBodies writes request/response bodies to disk using the
// request id from the Gin context. Failures are logged and never block billing.
func PersistRequestDetailBodies(c *gin.Context, requestBody, responseBody string) {
	if !common.RequestDetailLogEnabled {
		return
	}
	if c == nil {
		return
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		return
	}
	if err := WriteRequestDetailBodies(requestID, requestBody, responseBody); err != nil {
		logger.LogError(c, "failed to persist request detail bodies: "+err.Error())
	}
}

// WriteRequestDetailBodies stores request/response bodies under
// {log-dir}/request-detail/{request_id}/. Empty sides are skipped (existing
// files for a side are left untouched when that side is empty).
func WriteRequestDetailBodies(requestID, requestBody, responseBody string) error {
	if !common.RequestDetailLogEnabled {
		return nil
	}
	if strings.TrimSpace(requestBody) == "" && strings.TrimSpace(responseBody) == "" {
		return nil
	}
	dir, err := requestDetailDir(requestID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create request detail dir: %w", err)
	}
	if strings.TrimSpace(requestBody) != "" {
		path := filepath.Join(dir, requestDetailRequestFile)
		if err := os.WriteFile(path, []byte(requestBody), 0o640); err != nil {
			return fmt.Errorf("write request body file: %w", err)
		}
	}
	if strings.TrimSpace(responseBody) != "" {
		path := filepath.Join(dir, requestDetailResponseFile)
		if err := os.WriteFile(path, []byte(responseBody), 0o640); err != nil {
			return fmt.Errorf("write response body file: %w", err)
		}
	}
	return nil
}

// ReadRequestDetailBodies loads bodies for requestID from disk.
// Missing files yield empty strings without error.
func ReadRequestDetailBodies(requestID string) (RequestDetailBodies, error) {
	var out RequestDetailBodies
	dir, err := requestDetailDir(requestID)
	if err != nil {
		return out, err
	}
	reqPath := filepath.Join(dir, requestDetailRequestFile)
	if data, err := os.ReadFile(reqPath); err == nil {
		out.RequestBody = string(data)
	} else if !os.IsNotExist(err) {
		return out, fmt.Errorf("read request body file: %w", err)
	}
	respPath := filepath.Join(dir, requestDetailResponseFile)
	if data, err := os.ReadFile(respPath); err == nil {
		out.ResponseBody = string(data)
	} else if !os.IsNotExist(err) {
		return out, fmt.Errorf("read response body file: %w", err)
	}
	return out, nil
}

// ClearAllRequestDetailBodies removes the entire request-detail directory tree.
// Returns how many top-level request_id directories were removed.
func ClearAllRequestDetailBodies() (int, error) {
	root, err := requestDetailRoot()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return removed, fmt.Errorf("remove %s: %w", path, err)
		}
		removed++
	}
	return removed, nil
}

// RequestDetailStorageInfo returns basic disk usage for the request-detail root.
func RequestDetailStorageInfo() (dir string, fileCount int, totalSize int64, err error) {
	root, err := requestDetailRoot()
	if err != nil {
		return "", 0, 0, err
	}
	dir = root
	if _, statErr := os.Stat(root); os.IsNotExist(statErr) {
		return dir, 0, 0, nil
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		fileCount++
		totalSize += info.Size()
		return nil
	})
	return dir, fileCount, totalSize, err
}
