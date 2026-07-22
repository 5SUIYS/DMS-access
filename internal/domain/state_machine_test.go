package domain_test

// Feature: mysql-redshift-migration-approval, Property 9: 状态机非法转换拦截

import (
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/5miles/dms-access/internal/domain"
)

// allStatuses 包含所有工单状态。
var allStatuses = []domain.TicketStatus{
	domain.StatusDraft,
	domain.StatusPending,
	domain.StatusApproved,
	domain.StatusRejected,
	domain.StatusPlanReady,
	domain.StatusExecuting,
	domain.StatusCompleted,
	domain.StatusFailed,
}

// allActions 包含所有工单操作动作。
var allActions = []string{
	domain.ActionSubmit,
	domain.ActionApprove,
	domain.ActionReject,
	domain.ActionResubmit,
	domain.ActionGeneratePlan,
	domain.ActionExecute,
	domain.ActionStatusChange,
	domain.ActionStatusChangeFailed,
}

// legalTransitions 定义合法的（状态, 动作）→目标状态 映射，与 state_machine.go 保持同步。
// 用于属性测试的参考 oracle。
var legalTransitions = map[domain.TicketStatus]map[string]domain.TicketStatus{
	domain.StatusDraft:     {domain.ActionSubmit: domain.StatusPending},
	domain.StatusPending:   {domain.ActionApprove: domain.StatusApproved, domain.ActionReject: domain.StatusRejected},
	domain.StatusApproved:  {domain.ActionGeneratePlan: domain.StatusPlanReady},
	domain.StatusRejected:  {domain.ActionResubmit: domain.StatusPending},
	domain.StatusPlanReady: {domain.ActionExecute: domain.StatusExecuting},
	domain.StatusExecuting: {
		domain.ActionStatusChange:       domain.StatusCompleted,
		domain.ActionStatusChangeFailed: domain.StatusFailed,
	},
}

// TestLegalTransitions 验证所有合法转换成功执行，包含 executing→failed 路径。
func TestLegalTransitions(t *testing.T) {
	tests := []struct {
		current domain.TicketStatus
		action  string
		want    domain.TicketStatus
	}{
		{domain.StatusDraft, domain.ActionSubmit, domain.StatusPending},
		{domain.StatusPending, domain.ActionApprove, domain.StatusApproved},
		{domain.StatusPending, domain.ActionReject, domain.StatusRejected},
		{domain.StatusApproved, domain.ActionGeneratePlan, domain.StatusPlanReady},
		{domain.StatusRejected, domain.ActionResubmit, domain.StatusPending},
		{domain.StatusPlanReady, domain.ActionExecute, domain.StatusExecuting},
		// executing 可通过 status_change 进入 completed，通过 status_change_failed 进入 failed
		{domain.StatusExecuting, domain.ActionStatusChange, domain.StatusCompleted},
		{domain.StatusExecuting, domain.ActionStatusChangeFailed, domain.StatusFailed},
	}
	for _, tt := range tests {
		t.Run(string(tt.current)+"/"+tt.action, func(t *testing.T) {
			next, err := domain.ValidateTransition(tt.current, tt.action)
			require.NoError(t, err)
			assert.Equal(t, tt.want, next)
		})
	}
}

// TestIllegalTransitions 验证关键非法转换被拦截，返回 ErrInvalidStateTransition。
func TestIllegalTransitions(t *testing.T) {
	tests := []struct {
		current domain.TicketStatus
		action  string
	}{
		// approve/reject 仅在 pending 状态合法
		{domain.StatusDraft, domain.ActionApprove},
		{domain.StatusApproved, domain.ActionApprove},
		{domain.StatusDraft, domain.ActionReject},
		// generate_plan 仅在 approved 合法
		{domain.StatusPending, domain.ActionGeneratePlan},
		{domain.StatusDraft, domain.ActionGeneratePlan},
		// execute 仅在 plan_ready 合法
		{domain.StatusApproved, domain.ActionExecute},
		{domain.StatusPending, domain.ActionExecute},
		// resubmit 仅在 rejected 合法
		{domain.StatusDraft, domain.ActionResubmit},
		{domain.StatusPending, domain.ActionResubmit},
		// status_change 仅在 executing 合法
		{domain.StatusDraft, domain.ActionStatusChange},
		{domain.StatusPending, domain.ActionStatusChange},
		{domain.StatusCompleted, domain.ActionStatusChange},
		// status_change_failed 仅在 executing 合法
		{domain.StatusDraft, domain.ActionStatusChangeFailed},
		{domain.StatusApproved, domain.ActionStatusChangeFailed},
		// 终态不允许任何操作
		{domain.StatusCompleted, domain.ActionSubmit},
		{domain.StatusFailed, domain.ActionApprove},
		{domain.StatusFailed, domain.ActionExecute},
	}
	for _, tt := range tests {
		t.Run(string(tt.current)+"/"+tt.action, func(t *testing.T) {
			_, err := domain.ValidateTransition(tt.current, tt.action)
			assert.Error(t, err, "非法转换应返回错误")
			assert.ErrorIs(t, err, domain.ErrInvalidStateTransition)
		})
	}
}

// TestProperty9_StateMachineIllegalTransitions 属性测试：状态机非法转换拦截。
//
// Feature: mysql-redshift-migration-approval, Property 9: 状态机非法转换拦截
// Validates: Requirements 5.5, 5.4
//
// 对所有（状态，动作）组合枚举，合法转换放行并返回正确目标状态，非法转换返回 ErrInvalidStateTransition。
// 最少运行 100 次随机输入（gopter 默认 MinSuccessfulTests=100）。
func TestProperty9_StateMachineIllegalTransitions(t *testing.T) {
	params := gopter.DefaultTestParameters()
	params.MinSuccessfulTests = 100
	properties := gopter.NewProperties(params)

	// 生成随机状态索引（覆盖全部 8 个状态）
	genStatusIdx := gen.IntRange(0, len(allStatuses)-1)
	// 生成随机动作索引（覆盖全部 8 个动作）
	genActionIdx := gen.IntRange(0, len(allActions)-1)

	properties.Property("Property 9: 合法转换放行，非法转换返回 ErrInvalidStateTransition",
		prop.ForAll(
			func(statusIdx, actionIdx int) bool {
				status := allStatuses[statusIdx]
				action := allActions[actionIdx]

				next, err := domain.ValidateTransition(status, action)

				// 通过参考 oracle 判断该（状态, 动作）对是否合法
				legalActions, statusExists := legalTransitions[status]
				expectedNext, actionExists := legalActions[action]
				isLegal := statusExists && actionExists

				if isLegal {
					// 合法转换：err == nil 且目标状态与 oracle 一致
					if err != nil {
						return false
					}
					return next == expectedNext
				}
				// 非法转换：必须返回 ErrInvalidStateTransition，且 next 为零值
				if err == nil {
					return false
				}
				return domain.IsErrInvalidStateTransition(err) && next == ""
			},
			genStatusIdx,
			genActionIdx,
		),
	)

	properties.TestingRun(t)
}

// TestCanModify 验证 CanModify 只在 draft/rejected 状态下返回 true（Property 8 辅助函数）。
func TestCanModify(t *testing.T) {
	modifiable := []domain.TicketStatus{domain.StatusDraft, domain.StatusRejected}
	notModifiable := []domain.TicketStatus{
		domain.StatusPending, domain.StatusApproved, domain.StatusPlanReady,
		domain.StatusExecuting, domain.StatusCompleted, domain.StatusFailed,
	}
	for _, s := range modifiable {
		assert.True(t, domain.CanModify(s), "状态 %s 应可修改", s)
	}
	for _, s := range notModifiable {
		assert.False(t, domain.CanModify(s), "状态 %s 不应可修改", s)
	}
}

// TestIsTerminal 验证 IsTerminal 在 completed/failed 下返回 true，其余返回 false。
func TestIsTerminal(t *testing.T) {
	terminal := []domain.TicketStatus{domain.StatusCompleted, domain.StatusFailed}
	nonTerminal := []domain.TicketStatus{
		domain.StatusDraft, domain.StatusPending, domain.StatusApproved,
		domain.StatusRejected, domain.StatusPlanReady, domain.StatusExecuting,
	}
	for _, s := range terminal {
		assert.True(t, domain.IsTerminal(s), "状态 %s 应为终态", s)
	}
	for _, s := range nonTerminal {
		assert.False(t, domain.IsTerminal(s), "状态 %s 不应为终态", s)
	}
}

// TestErrInvalidStateTransitionMessage 验证错误消息包含状态和动作信息。
func TestErrInvalidStateTransitionMessage(t *testing.T) {
	_, err := domain.ValidateTransition(domain.StatusCompleted, domain.ActionApprove)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrInvalidStateTransition)
	// 错误消息中应包含状态名，便于调试
	errMsg := err.Error()
	assert.Contains(t, errMsg, "completed")
}
