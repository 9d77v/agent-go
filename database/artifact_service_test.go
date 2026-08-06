package database

import (
	"context"
	"testing"

	"google.golang.org/adk/v2/artifact"
	"google.golang.org/genai"
)

const testImageURL = "https://hhy-code-oss.oss-cn-shanghai.aliyuncs.com/default/artifacts/images/50/ab/62/50ab6248a0c864edb6db8ed785dac312c7d47ddeebdf283403d9409c1f36b603.webp"

// TestStartupCreatesArtifactTables 验证启动（NewDBService）即建表：
func TestStartupCreatesArtifactTables(t *testing.T) {
	svc := newTestDBService(t) // newTestDBService 内部即 NewDBService，不触发 GetArtifactService
	for _, table := range []string{"blob_files", "artifacts"} {
		if !svc.DB().Migrator().HasTable(table) {
			t.Errorf("启动后缺少表 %s", table)
		}
	}
}

func TestExtractSHA256FromURL(t *testing.T) {
	if got := ExtractSHA256FromURL(testImageURL); got != "50ab6248a0c864edb6db8ed785dac312c7d47ddeebdf283403d9409c1f36b603" {
		t.Errorf("ExtractSHA256FromURL = %q", got)
	}
	if got := ExtractSHA256FromURL("https://example.com/a.jpg"); got != "" {
		t.Errorf("非标准 URL 应返回空, got %q", got)
	}
	if got := ExtractSHA256FromURL(""); got != "" {
		t.Errorf("空串应返回空, got %q", got)
	}
}

func TestBlobFileUpsertDedup(t *testing.T) {
	svc := newTestDBService(t)

	sha := "50ab6248a0c864edb6db8ed785dac312c7d47ddeebdf283403d9409c1f36b603"
	// 同一 sha256 两次 Upsert → 只一条记录
	if err := svc.UpsertBlobFile(sha, testImageURL, "image/webp", 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpsertBlobFile(sha, testImageURL, "image/webp", 200); err != nil {
		t.Fatal(err)
	}
	blob, ok, err := svc.FindBlobBySHA256(sha)
	if err != nil || !ok {
		t.Fatalf("FindBlobBySHA256: ok=%v err=%v", ok, err)
	}
	if blob.Size != 200 {
		t.Errorf("upsert 应更新 size, got %d", blob.Size)
	}

	// 同一 sha256 只一条
	var count int64
	svc.DB().Model(&BlobFile{}).Where("sha256 = ?", sha).Count(&count)
	if count != 1 {
		t.Errorf("blob_files 应只有 1 条, got %d", count)
	}
}

func TestArtifactDedupByBlobSHA256(t *testing.T) {
	svc := newTestDBService(t)
	arts := svc.GetArtifactService()

	sessionID := "sess-1"
	save := func(name string) {
		if _, err := arts.Save(context.Background(), &artifact.SaveRequest{
			AppName:   "hhycode",
			UserID:    "default",
			SessionID: sessionID,
			FileName:  name,
			Part:      &genai.Part{Text: testImageURL},
		}); err != nil {
			t.Fatal(err)
		}
	}
	save("artifact_1_0")
	// 再次保存同 URL → 查重应命中第一条
	found, ok, err := svc.FindArtifactByBlobSHA256("hhycode", "default", sessionID, ExtractSHA256FromURL(testImageURL))
	if err != nil || !ok {
		t.Fatalf("FindArtifactByBlobSHA256: ok=%v err=%v", ok, err)
	}
	if found != "artifact_1_0" {
		t.Errorf("应命中已存在文件名, got %q", found)
	}
}
