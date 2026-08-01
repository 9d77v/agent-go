// Package credential provides Windows Credential Manager integration.
package credential

import "strings"

// Manager provides credential storage and retrieval.
type Manager struct {
	prefix   string
	userName string
}

// New creates a credential manager with the given target prefix.
// The prefix is used to namespace credentials, e.g. "MyApp/LLM/".
func New(prefix, userName string) *Manager {
	return &Manager{prefix: prefix, userName: userName}
}

// Save stores a credential.
func (m *Manager) Save(id, secret string) error {
	return winCredWrite(m.prefix+id, m.userName, secret)
}

// Get retrieves a credential.
func (m *Manager) Get(id string) (string, error) {
	return winCredRead(m.prefix + id)
}

// Delete removes a credential.
func (m *Manager) Delete(id string) error {
	return winCredDelete(m.prefix + id)
}

// List 枚举目标名以 m.prefix+prefix 开头的凭据，返回去掉 m.prefix 后的相对标识。
// 用于清理已无对应配置的无效凭据。
func (m *Manager) List(prefix string) ([]string, error) {
	targets, err := winCredEnumerate(m.prefix + prefix)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(targets))
	for _, t := range targets {
		result = append(result, strings.TrimPrefix(t, m.prefix))
	}
	return result, nil
}
