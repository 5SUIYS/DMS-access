package auth

import "time"

// Observer 提供认证过程的可观测性挂钩（指标/日志）。
// 以接口形式解耦具体指标实现（Prometheus 等），默认无操作。
type Observer interface {
	// ObserveJWKSFetch 记录一次 JWKS 拉取的耗时与结果（err 非 nil 表示失败/超时）。
	ObserveJWKSFetch(d time.Duration, err error)
	// IncAuthUnavailable 记录一次"认证不可用"事件。
	IncAuthUnavailable()
}

// nopObserver 是默认的无操作 Observer。
type nopObserver struct{}

func (nopObserver) ObserveJWKSFetch(time.Duration, error) {}
func (nopObserver) IncAuthUnavailable()                   {}

// NopObserver 返回一个不做任何记录的 Observer，供未接入指标系统时使用。
func NopObserver() Observer { return nopObserver{} }
