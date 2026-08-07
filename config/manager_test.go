package config

import "testing"

func newTestManager(t *testing.T) *ConfigManager {
	t.Helper()
	cm := NewConfigManager(t.TempDir())
	if err := cm.Load(); err != nil {
		t.Fatal(err)
	}
	return cm
}

// TestAddGetLLMProviders 特征化：添加 provider 后默认 ID 设为第一个。
func TestAddGetLLMProviders(t *testing.T) {
	cm := newTestManager(t)
	if _, err := cm.AddLLMProvider(map[string]any{"id": "p1", "name": "DeepSeek"}); err != nil {
		t.Fatal(err)
	}
	providers := cm.GetLLMProviders()
	if len(providers) != 1 || providers[0]["id"] != "p1" {
		t.Fatalf("providers = %v", providers)
	}
	if id := cm.GetDefaultLLMID(); id != "p1" {
		t.Errorf("default = %q, want p1", id)
	}
}

// TestUpdateDeleteLLMProvider 特征化：更新与删除 provider。
func TestUpdateDeleteLLMProvider(t *testing.T) {
	cm := newTestManager(t)
	if _, err := cm.AddLLMProvider(map[string]any{"id": "p1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cm.AddLLMProvider(map[string]any{"id": "p2"}); err != nil {
		t.Fatal(err)
	}
	if err := cm.UpdateLLMProvider("p1", map[string]any{"id": "p1", "name": "updated"}); err != nil {
		t.Fatal(err)
	}
	if err := cm.DeleteLLMProvider("p1"); err != nil {
		t.Fatal(err)
	}
	providers := cm.GetLLMProviders()
	if len(providers) != 1 || providers[0]["id"] != "p2" {
		t.Fatalf("providers = %v", providers)
	}
}

// TestModelCache 特征化：模型缓存的写入与读取。
func TestModelCache(t *testing.T) {
	cm := newTestManager(t)
	if _, err := cm.AddLLMProvider(map[string]any{"id": "p1"}); err != nil {
		t.Fatal(err)
	}
	models := []any{"m1", "m2"}
	if err := cm.SetCachedModels("p1", models); err != nil {
		t.Fatal(err)
	}
	got := cm.GetCachedModels("p1")
	if len(got) != 2 || got[0] != "m1" {
		t.Fatalf("cached = %v", got)
	}
	if cm.GetCachedModels("nope") != nil {
		t.Errorf("cached for unknown provider should be nil")
	}
}

// TestLocalSettings 特征化：本地设置的读写。
func TestLocalSettings(t *testing.T) {
	cm := newTestManager(t)
	if err := cm.SetLocalString("root", "C:/ws"); err != nil {
		t.Fatal(err)
	}
	if v := cm.GetLocalString("root"); v != "C:/ws" {
		t.Errorf("local = %q, want C:/ws", v)
	}
	if err := cm.SetLocalAny("obj", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if v := cm.GetLocalAny("obj"); v == nil {
		t.Errorf("local any missing")
	}
}

// TestSetSettingUnknownName 特征化：未知配置文件名静默忽略。
func TestSetSettingUnknownName(t *testing.T) {
	cm := newTestManager(t)
	if err := cm.SetSetting("unknown", map[string]any{"x": 1}); err != nil {
		t.Errorf("unknown name should return nil, got %v", err)
	}
}
