// Package config provides a generic dual-file JSON configuration manager.
// It uses two files: config.json (for machine-synced settings) and
// local_settings.json (for machine-local settings).
// Application-specific types should be added via GetSetting/SetSetting or
// by wrapping this manager in the application layer.
package config

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

// ConfigManager manages two JSON config files:
//   - config.json: cross-machine settings (e.g., LLM providers)
//   - local_settings.json: machine-local settings (e.g., paths, preferences)
type ConfigManager struct {
	mu            sync.RWMutex
	filePath      string // config.json
	localFilePath string // local_settings.json
	config        map[string]any
	local         map[string]any
}

// NewConfigManager creates a ConfigManager for the given app data directory.
func NewConfigManager(appDataDir string) *ConfigManager {
	return &ConfigManager{
		filePath:      filepath.Join(appDataDir, "config.json"),
		localFilePath: filepath.Join(appDataDir, "local_settings.json"),
		config:        defaultConfig(),
		local:         defaultLocal(),
	}
}

func defaultConfig() map[string]any {
	return map[string]any{
		"llm_providers":  []any{},
		"default_llm_id": "",
		"agent_settings": map[string]any{},
	}
}

func defaultLocal() map[string]any {
	return map[string]any{}
}

// Load reads both config files from disk. If config.json doesn't exist,
// it creates one with defaults and saves it.
func (cm *ConfigManager) Load() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Load config.json
	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			cm.config = defaultConfig()
			if err := cm.save(); err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
		if cfg == nil {
			cfg = defaultConfig()
		}
		if cfg["llm_providers"] == nil {
			cfg["llm_providers"] = []any{}
		}
		cm.config = cfg
	}

	// Load local_settings.json
	cm.local = defaultLocal()
	if localData, err := os.ReadFile(cm.localFilePath); err == nil {
		var local map[string]any
		if json.Unmarshal(localData, &local) == nil && local != nil {
			cm.local = local
		}
	}
	return nil
}

// Save persists the config.json to disk.
func (cm *ConfigManager) Save() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.save()
}

// save writes config.json (caller must hold mu).
func (cm *ConfigManager) save() error {
	dir := filepath.Dir(cm.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cm.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cm.filePath, data, 0644)
}

// saveLocal writes local_settings.json (caller must hold mu).
func (cm *ConfigManager) saveLocal() error {
	dir := filepath.Dir(cm.localFilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cm.local, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cm.localFilePath, data, 0644)
}

// GetConfig returns a snapshot of the entire config.json as a map.
func (cm *ConfigManager) GetConfig() map[string]any {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	out := make(map[string]any, len(cm.config))
	maps.Copy(out, cm.config)
	return out
}

// GetSetting returns the raw content of a config file as a flat map.
// name: "config" → config.json, "local_settings" → local_settings.json.
func (cm *ConfigManager) GetSetting(name string) map[string]any {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	switch name {
	case "config":
		out := make(map[string]any, len(cm.config))
		maps.Copy(out, cm.config)
		return out
	case "local_settings":
		out := make(map[string]any, len(cm.local))
		maps.Copy(out, cm.local)
		return out
	default:
		return map[string]any{}
	}
}

// SetSetting merges data into the specified config file and saves it.
// name: "config" → config.json, "local_settings" → local_settings.json.
// Merges top-level keys only; nested maps are replaced, not deep-merged.
func (cm *ConfigManager) SetSetting(name string, data map[string]any) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	switch name {
	case "config":
		maps.Copy(cm.config, data)
		return cm.save()
	case "local_settings":
		maps.Copy(cm.local, data)
		return cm.saveLocal()
	default:
		return nil
	}
}

// --- LLM Provider helpers ---

// AddLLMProvider adds an LLM provider config and returns its generated ID.
func (cm *ConfigManager) AddLLMProvider(p map[string]any) (string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	providers, _ := cm.config["llm_providers"].([]any)
	providers = append(providers, p)
	cm.config["llm_providers"] = providers

	if cm.config["default_llm_id"] == "" || cm.config["default_llm_id"] == nil {
		if id, ok := p["id"].(string); ok {
			cm.config["default_llm_id"] = id
		}
	}
	return "", cm.save()
}

// UpdateLLMProvider updates an existing LLM provider by ID.
func (cm *ConfigManager) UpdateLLMProvider(id string, p map[string]any) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	providers, _ := cm.config["llm_providers"].([]any)
	for i, v := range providers {
		if prov, ok := v.(map[string]any); ok {
			if prov["id"] == id {
				providers[i] = p
				cm.config["llm_providers"] = providers
				return cm.save()
			}
		}
	}
	return nil
}

// DeleteLLMProvider removes an LLM provider by ID.
func (cm *ConfigManager) DeleteLLMProvider(id string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	providers, _ := cm.config["llm_providers"].([]any)
	idx := -1
	for i, v := range providers {
		if prov, ok := v.(map[string]any); ok {
			if prov["id"] == id {
				idx = i
				break
			}
		}
	}
	if idx == -1 {
		return nil
	}
	cm.config["llm_providers"] = append(providers[:idx], providers[idx+1:]...)

	if cm.config["default_llm_id"] == id {
		if remaining, ok := cm.config["llm_providers"].([]any); ok && len(remaining) > 0 {
			if first, ok := remaining[0].(map[string]any); ok {
				cm.config["default_llm_id"] = first["id"]
			}
		} else {
			cm.config["default_llm_id"] = ""
		}
	}
	return cm.save()
}

// GetLLMProviders returns all LLM providers as a slice.
func (cm *ConfigManager) GetLLMProviders() []map[string]any {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	providers, _ := cm.config["llm_providers"].([]any)
	result := make([]map[string]any, 0, len(providers))
	for _, v := range providers {
		if p, ok := v.(map[string]any); ok {
			result = append(result, p)
		}
	}
	return result
}

// SetDefaultLLM sets the default LLM provider ID.
func (cm *ConfigManager) SetDefaultLLM(id string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.config["default_llm_id"] = id
	return cm.save()
}

// GetDefaultLLMID returns the default LLM provider ID.
func (cm *ConfigManager) GetDefaultLLMID() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	id, _ := cm.config["default_llm_id"].(string)
	return id
}

// --- Model cache helpers ---

// SetCachedModels caches model list for a provider.
func (cm *ConfigManager) SetCachedModels(providerID string, models []any) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	providers, _ := cm.config["llm_providers"].([]any)
	for i, v := range providers {
		prov, ok := v.(map[string]any)
		if !ok || prov["id"] != providerID {
			continue
		}
		prov["cached_models"] = models
		providers[i] = prov
		cm.config["llm_providers"] = providers
		return cm.save()
	}
	return nil
}

// GetCachedModels returns cached models for a provider.
func (cm *ConfigManager) GetCachedModels(providerID string) []any {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	providers, _ := cm.config["llm_providers"].([]any)
	for _, v := range providers {
		if prov, ok := v.(map[string]any); ok && prov["id"] == providerID {
			if models, ok := prov["cached_models"].([]any); ok {
				return models
			}
			return nil
		}
	}
	return nil
}

// SetSelectedModels saves selected model IDs for a provider.
func (cm *ConfigManager) SetSelectedModels(providerID string, models []any) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	providers, _ := cm.config["llm_providers"].([]any)
	for i, v := range providers {
		prov, ok := v.(map[string]any)
		if !ok || prov["id"] != providerID {
			continue
		}
		prov["selected_models"] = models
		providers[i] = prov
		cm.config["llm_providers"] = providers
		return cm.save()
	}
	return nil
}

// GetSelectedModels returns selected model IDs for a provider.
func (cm *ConfigManager) GetSelectedModels(providerID string) []any {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	providers, _ := cm.config["llm_providers"].([]any)
	for _, v := range providers {
		if prov, ok := v.(map[string]any); ok && prov["id"] == providerID {
			if models, ok := prov["selected_models"].([]any); ok {
				return models
			}
			return nil
		}
	}
	return nil
}

// --- Local setting helpers ---

// GetLocalString returns a string from local_settings.json.
func (cm *ConfigManager) GetLocalString(key string) string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	v, _ := cm.local[key].(string)
	return v
}

// SetLocalString sets a string in local_settings.json and saves.
func (cm *ConfigManager) SetLocalString(key, value string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.local[key] = value
	return cm.saveLocal()
}

// GetLocalAny returns a value from local_settings.json by key.
func (cm *ConfigManager) GetLocalAny(key string) any {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.local[key]
}

// SetLocalAny sets a value in local_settings.json and saves.
func (cm *ConfigManager) SetLocalAny(key string, value any) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.local[key] = value
	return cm.saveLocal()
}
