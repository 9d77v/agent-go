// Package credential provides Windows Credential Manager integration.
package credential

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
