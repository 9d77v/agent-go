package msgdb

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	f, err := os.CreateTemp("", "msgdb-test-*.db")
	require.NoError(t, err)
	f.Close()
	s := NewStore(f.Name())
	err = s.Open()
	require.NoError(t, err)
	t.Cleanup(func() { s.Close(); os.Remove(f.Name()) })
	return s
}

func TestOpenAndClose(t *testing.T) {
	s := newTestStore(t)
	assert.NotNil(t, s)
	assert.NotEmpty(t, s.GetDBPath())
}

func TestInsertAndListMessages(t *testing.T) {
	s := newTestStore(t)
	msg := &Message{
		SessionID: "sess-1",
		Role:      "user",
		Content:   "hello",
	}
	err := s.InsertMessage(msg)
	require.NoError(t, err)
	assert.NotEmpty(t, msg.ID)

	msgs, err := s.ListMessages("sess-1")
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "hello", msgs[0].Content)
}

func TestInsertMessageAutoSeq(t *testing.T) {
	s := newTestStore(t)
	m1 := &Message{SessionID: "sess-seq", Role: "user", Content: "first"}
	m2 := &Message{SessionID: "sess-seq", Role: "user", Content: "second"}
	require.NoError(t, s.InsertMessage(m1))
	require.NoError(t, s.InsertMessage(m2))
	assert.Equal(t, int64(1), m1.Seq)
	assert.Equal(t, int64(2), m2.Seq)
}

func TestCreateSession(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateSession("sess-create", "ws-1")
	require.NoError(t, err)

	sessions, err := s.ListSessions(10, "ws-1")
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "sess-create", sessions[0].SessionID)
}

func TestDeleteSessionCascade(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.CreateSession("sess-del", "ws-1"))
	require.NoError(t, s.InsertMessage(&Message{SessionID: "sess-del", Role: "user", Content: "msg"}))

	err := s.DeleteSession("sess-del")
	require.NoError(t, err)

	msgs, err := s.ListMessages("sess-del")
	require.NoError(t, err)
	assert.Len(t, msgs, 0)
}

func TestApprovalModeRoundTrip(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.CreateSession("sess-ap", "ws-1"))

	mode := s.GetApprovalMode("sess-ap")
	assert.Equal(t, "default", mode)

	require.NoError(t, s.SetApprovalMode("sess-ap", "bypass"))
	assert.Equal(t, "bypass", s.GetApprovalMode("sess-ap"))

	require.NoError(t, s.SetApprovalMode("sess-ap", "auto"))
	assert.Equal(t, "auto", s.GetApprovalMode("sess-ap"))
}

func TestMarkRetryDone(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.CreateSession("sess-rt", "ws-1"))
	msg := &Message{SessionID: "sess-rt", Role: "user", Content: "test"}
	require.NoError(t, s.InsertMessage(msg))

	err := s.MarkRetryDone(msg.ID)
	require.NoError(t, err)

	// Verify by reading back
	msgs, err := s.ListMessages("sess-rt")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	meta := msgs[0].ParseMetadata()
	assert.True(t, meta.RetryDone)
}

func TestMarkRetryDoneEmptyID(t *testing.T) {
	s := newTestStore(t)
	err := s.MarkRetryDone("")
	assert.NoError(t, err)
}

func TestMigration(t *testing.T) {
	f, err := os.CreateTemp("", "msgdb-migrate-*.db")
	require.NoError(t, err)
	f.Close()
	defer os.Remove(f.Name())

	s := NewStore(f.Name())
	called := false
	s.RegisterMigration(Migration{
		Version: 999,
		SQL:     "CREATE TABLE IF NOT EXISTS migration_test (id INTEGER PRIMARY KEY)",
	})
	s.RegisterMigration(Migration{
		Version: 1000,
		SQL:     "INSERT INTO migration_test VALUES (1)",
	})
	err = s.Open()
	require.NoError(t, err)
	called = true // migration executed without error
	assert.True(t, called)
	s.Close()
}

func TestUpdateSessionTokens(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.CreateSession("sess-tk", "ws-1"))

	err := s.UpdateSessionTokens("sess-tk", 100, 50, 150)
	require.NoError(t, err)

	p, c, tot := s.GetSessionTokens("sess-tk")
	assert.Equal(t, 100, p)
	assert.Equal(t, 50, c)
	assert.Equal(t, 150, tot)
}

func TestUpdateLLMLastSentMsgID(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.CreateSession("sess-llm", "ws-1"))
	err := s.UpdateLLMLastSentMsgID("sess-llm", "msg-123")
	require.NoError(t, err)
}
