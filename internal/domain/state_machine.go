package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidStateTransition 表示工单状态机的非法转换（需求 5.5，Property 9）。
var ErrInvalidStateTransition = errors.New("domain: 非法的工单状态转换")

// 工单操作动作常量。
const (
	ActionSubmit             = "submit"
	ActionApprove            = "approve"
	ActionReject             = "reject"
	ActionResubmit           = "resubmit"
	ActionGeneratePlan       = "generate_plan"
	ActionExecute            = "execute"
	ActionStatusChange       = "status_change"        // executing → completed（轮询成功）
	ActionStatusChangeFailed = "status_change_failed" // executing → failed（轮询失败）
)

// validTransitions 定义合法的状态转换表：map[当前状态]map[动作]目标状态。
var validTransitions = map[TicketStatus]map[string]TicketStatus{
	StatusDraft: {
		ActionSubmit: StatusPending,
	},
	StatusPending: {
		ActionApprove: StatusApproved,
		ActionReject:  StatusRejected,
	},
	StatusApproved: {
		ActionGeneratePlan: StatusPlanReady,
	},
	StatusRejected: {
		ActionResubmit: StatusPending,
	},
	StatusPlanReady: {
		ActionExecute: StatusExecuting,
	},
	StatusExecuting: {
		ActionStatusChange:       StatusCompleted, // 轮询成功 → completed
		ActionStatusChangeFailed: StatusFailed,    // 轮询失败 → failed
	},
	// completed 与 failed 为终态，不允许转换。
}

// ValidateTransition 检查从 current 状态执行 action 是否合法（Property 9）。
// 合法时返回目标状态，非法时返回 ErrInvalidStateTransition。
func ValidateTransition(current TicketStatus, action string) (TicketStatus, error) {
	actions, ok := validTransitions[current]
	if !ok {
		return "", fmt.Errorf("%w: 状态 %q 不支持任何操作", ErrInvalidStateTransition, current)
	}
	next, ok := actions[action]
	if !ok {
		return "", fmt.Errorf("%w: 状态 %q 不允许执行动作 %q", ErrInvalidStateTransition, current, action)
	}
	return next, nil
}

// CanModify 判断工单是否可以修改（仅 draft/rejected 状态允许，Property 8）。
func CanModify(status TicketStatus) bool {
	return status == StatusDraft || status == StatusRejected
}

// IsTerminal 判断工单是否处于终态（completed/failed）。
func IsTerminal(status TicketStatus) bool {
	return status == StatusCompleted || status == StatusFailed
}

// HasPreconditionErrors 检查前置条件中是否存在 error 级别的警告（Property 11）。
func HasPreconditionErrors(warnings PreconditionWarnings) bool {
	for _, w := range warnings {
		if w.Level == "error" {
			return true
		}
	}
	return false
}

// IsErrInvalidStateTransition 判断 err 是否为 ErrInvalidStateTransition。
func IsErrInvalidStateTransition(err error) bool {
	return errors.Is(err, ErrInvalidStateTransition)
}
