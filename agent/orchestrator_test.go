package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ftool "github.com/9d77v/agent-go/tool"
)

func TestNewOrchestrator(t *testing.T) {
	o := &Orchestrator{
		config: OrchestratorConfig{},
	}
	assert.NotNil(t, o)
}

func TestApprovalLifecycle(t *testing.T) {
	o := &Orchestrator{
		pendingApprovals: make(map[string]chan ApprovalResult),
	}

	// Register
	ch := o.RegisterApproval("approval-1")
	assert.NotNil(t, ch)

	// Resolve
	go func() {
		o.ResolveApproval("approval-1", true)
	}()

	select {
	case result := <-ch:
		assert.True(t, result.Approved)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approval")
	}
}

func TestRegisterAndResolveApproval(t *testing.T) {
	o := &Orchestrator{
		pendingApprovals: make(map[string]chan ApprovalResult),
	}

	ch := o.RegisterApproval("test-approval")
	o.ResolveApproval("test-approval", false)

	select {
	case result := <-ch:
		assert.False(t, result.Approved)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for approval result")
	}
}

func TestResolveApprovalNoChannel(t *testing.T) {
	o := &Orchestrator{
		pendingApprovals: make(map[string]chan ApprovalResult),
	}
	// Should not panic
	o.ResolveApproval("nonexistent", true)
}

func TestQuestionnaireLifecycle(t *testing.T) {
	o := &Orchestrator{
		pendingQuestionnaires: make(map[string]chan string),
	}

	ch := o.RegisterQuestionnaire("q-1")
	assert.NotNil(t, ch)

	go func() {
		o.ResolveQuestionnaire("q-1", `{"answer":"yes"}`)
	}()

	select {
	case result := <-ch:
		assert.Equal(t, `{"answer":"yes"}`, result)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for questionnaire")
	}
}

func TestRegisterAndResolveQuestionnaire(t *testing.T) {
	o := &Orchestrator{
		pendingQuestionnaires: make(map[string]chan string),
	}

	ch := o.RegisterQuestionnaire("test-q")
	o.ResolveQuestionnaire("test-q", `{"color":"blue"}`)

	select {
	case result := <-ch:
		assert.Equal(t, `{"color":"blue"}`, result)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for questionnaire result")
	}
}

func TestBuildRetryMessage(t *testing.T) {
	tests := []struct {
		name       string
		errType    AgentErrorType
		maxRetries int
		retries    int
	}{
		{"compile_fail", AgentErrCompileFail, 3, 1},
		{"tool_fail", AgentErrToolFail, 3, 2},
		{"default", AgentErrNone, 3, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := &AgentErrorState{
				ErrType: tt.errType,
				Retries: tt.retries,
			}
			msg := buildRetryMessage(es, tt.maxRetries)
			assert.Contains(t, msg, "次")
			assert.Contains(t, msg, "/")
		})
	}
}

func TestSetSubApprovalCallback(t *testing.T) {
	o := &Orchestrator{}
	called := false
	o.SetSubApprovalCallback(func(approvalID, command string, riskLevel ftool.RiskLevel) {
		called = true
	})
	require.NotNil(t, o.onSubApprovalRequest)
	o.onSubApprovalRequest("id", "cmd", ftool.RiskSafe)
	assert.True(t, called)
}

func TestOrchestratorConfigDefaults(t *testing.T) {
	cfg := OrchestratorConfig{}
	assert.Equal(t, 0, cfg.IterationsPerBatch)
	assert.Equal(t, 0, cfg.ContextWindow)
	assert.Nil(t, cfg.TitleGenerator)
}
