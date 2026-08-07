package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 测试审批攒批：同一编排批次多个审批全部已决后才统一恢复，
// 一次提交全部决策（ADK 需一次响应所有确认才能并发执行全部工具）。

func TestResolveApproval_Single(t *testing.T) {
	sm := NewStreamManager(nil)
	sm.recordApproval("s1", "appr_1")

	decisions, resume, ok := sm.ResolveApproval("s1", "appr_1", true)
	assert.True(t, ok, "已记录的审批应被识别")
	assert.True(t, resume, "单个审批已决后应立即恢复")
	assert.Equal(t, []ApprovalDecision{{ApprovalID: "appr_1", Approved: true}}, decisions)
}

func TestResolveApproval_BatchWaitsForAll(t *testing.T) {
	sm := NewStreamManager(nil)
	sm.recordApproval("s1", "appr_1")
	sm.recordApproval("s1", "appr_2")

	// 第一个已决：同批还有未决 → 不恢复
	decisions, resume, ok := sm.ResolveApproval("s1", "appr_1", true)
	assert.True(t, ok)
	assert.False(t, resume, "同批还有未决审批时必须等待")
	assert.Nil(t, decisions)

	// 第二个已决：全部通过 → 一次恢复全部（含拒绝项 confirmed=false）
	decisions, resume, ok = sm.ResolveApproval("s1", "appr_2", false)
	assert.True(t, ok)
	assert.True(t, resume, "同批全部已决后应统一恢复")
	assert.Len(t, decisions, 2)
	byID := map[string]bool{}
	for _, d := range decisions {
		byID[d.ApprovalID] = d.Approved
	}
	assert.Equal(t, map[string]bool{"appr_1": true, "appr_2": false}, byID)

	// 批已消费：再次解析同一 approval 应报未知
	_, resume, ok = sm.ResolveApproval("s1", "appr_1", true)
	assert.False(t, ok, "已消费的审批不应重复恢复")
	assert.False(t, resume)
}

func TestResolveApproval_Unknown(t *testing.T) {
	sm := NewStreamManager(nil)
	_, _, ok := sm.ResolveApproval("s1", "ghost", true)
	assert.False(t, ok, "未知审批应报错（已恢复或超时）")
}

func TestResolveApproval_ResetOnNewOrchestration(t *testing.T) {
	sm := NewStreamManager(nil)
	sm.recordApproval("s1", "appr_1")
	sm.resetApprovals("s1") // 新编排轮开始，旧批失效

	_, _, ok := sm.ResolveApproval("s1", "appr_1", true)
	assert.False(t, ok, "新编排轮后旧批审批应失效")
}

func TestResolveApproval_ChainAcrossSessions(t *testing.T) {
	sm := NewStreamManager(nil)
	sm.recordApproval("s1", "appr_1")
	sm.recordApproval("s2", "appr_2")

	// 不同 session 的审批互不影响
	_, resume, ok := sm.ResolveApproval("s2", "appr_2", true)
	assert.True(t, ok)
	assert.True(t, resume, "s2 无同批未决，应立即恢复")

	_, resume, ok = sm.ResolveApproval("s1", "appr_1", true)
	assert.True(t, ok)
	assert.True(t, resume)
}
