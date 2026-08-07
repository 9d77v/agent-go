package stream

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newTestWSServer 构造一个仅含文件服务目录的 WSServer（sm 可为 nil，ServeFile 不使用）。
func newTestWSServer(t *testing.T) (*WSServer, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewWSServer(nil, dir), dir
}

// TestServeFile_ServesFileInsideDir 特征化正常文件服务。
func TestServeFile_ServesFileInsideDir(t *testing.T) {
	ws, _ := newTestWSServer(t)
	req := httptest.NewRequest(http.MethodGet, "/files/0/hello.txt", nil)
	rec := httptest.NewRecorder()
	ws.ServeFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "hi" {
		t.Errorf("body = %q, want hi", rec.Body.String())
	}
}

// TestServeFile_PathTraversalBlocked 安全特征化：../ 路径穿越必须被拒绝。
func TestServeFile_PathTraversalBlocked(t *testing.T) {
	ws, _ := newTestWSServer(t)
	secret := t.TempDir()
	if err := os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/files/0/../"+filepath.Base(secret)+"/secret.txt", nil)
	rec := httptest.NewRecorder()
	ws.ServeFile(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403 (path traversal must be blocked)", rec.Code)
	}
}

// TestServeFile_InvalidIndex 特征化越界目录索引 → 404。
func TestServeFile_InvalidIndex(t *testing.T) {
	ws, _ := newTestWSServer(t)
	req := httptest.NewRequest(http.MethodGet, "/files/99/x.txt", nil)
	rec := httptest.NewRecorder()
	ws.ServeFile(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", rec.Code)
	}
}

// TestServeFile_MissingPathSegment 特征化缺少文件路径段 → 404。
func TestServeFile_MissingPathSegment(t *testing.T) {
	ws, _ := newTestWSServer(t)
	req := httptest.NewRequest(http.MethodGet, "/files/0", nil)
	rec := httptest.NewRecorder()
	ws.ServeFile(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", rec.Code)
	}
}
