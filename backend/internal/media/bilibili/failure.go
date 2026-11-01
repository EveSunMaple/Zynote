package bilibili

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const defaultCooldown = 30 * time.Second
const maximumCooldown = 5 * time.Minute

func biliTransportFailure(err error) *contract.AppError {
	var application *contract.AppError
	if errors.As(err, &application) {
		return application
	}
	if errors.Is(err, context.Canceled) {
		return contract.NewError(contract.CodeCancelled, "B 站请求已取消")
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()) {
		return contract.NewError(contract.CodeTimeout, "B 站请求超时，请稍后重试")
	}
	return contract.Unavailable("B 站服务暂不可用，请稍后重试")
}

func biliHTTPFailure(status int) *contract.AppError {
	switch status {
	case http.StatusUnauthorized:
		return contract.NewError(contract.CodeAuthExpired, "B 站登录已失效，请重新扫码")
	case http.StatusTooManyRequests:
		return contract.NewError(contract.CodeRateLimited, "B 站请求过于频繁，请稍后重试")
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return contract.NewError(contract.CodeTimeout, "B 站请求超时，请稍后重试")
	default:
		return contract.Unavailable(fmt.Sprintf("B 站服务暂不可用（HTTP %d）", status))
	}
}

func biliCodeFailure(code int) *contract.AppError {
	switch code {
	case -101:
		return contract.NewError(contract.CodeAuthExpired, "B 站登录已失效，请重新扫码")
	case -412, -509:
		return contract.NewError(contract.CodeRateLimited, "B 站请求受限，请稍后重试")
	default:
		return contract.Unavailable(fmt.Sprintf("B 站读取失败（%d），请稍后重试", code))
	}
}

func (resources *Resources) currentTime() time.Time {
	if resources.now != nil {
		return resources.now()
	}
	return time.Now()
}

func (resources *Resources) rateLimitFailure() error {
	now := resources.currentTime()
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if !resources.cooldownUntil.After(now) {
		return nil
	}
	return &contract.AppError{Code: contract.CodeRateLimited, Message: "B 站请求冷却中，请稍后重试", Data: map[string]any{"retryAfterSeconds": int(math.Ceil(resources.cooldownUntil.Sub(now).Seconds()))}}
}

func cooldownDuration(value string, now time.Time) time.Duration {
	duration := defaultCooldown
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		// Clamp before converting to Duration to avoid integer overflow.
		if seconds > int64(maximumCooldown/time.Second) {
			return maximumCooldown
		}
		if seconds > 0 {
			duration = time.Duration(seconds) * time.Second
		}
	} else if deadline, err := http.ParseTime(value); err == nil && deadline.After(now) {
		duration = deadline.Sub(now)
	}
	if duration > maximumCooldown {
		return maximumCooldown
	}
	return duration
}

func (resources *Resources) recordRateLimit(header http.Header) {
	now := resources.currentTime()
	until := now.Add(cooldownDuration(header.Get("Retry-After"), now))
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if until.After(resources.cooldownUntil) {
		resources.cooldownUntil = until
	}
}

func recordSubtitleRateLimit(client *http.Client, header http.Header) {
	if client == nil {
		return
	}
	if transport, ok := client.Transport.(sessionTransport); ok {
		transport.resources.recordRateLimit(header)
	}
}

func subtitleCodeFailure(client *http.Client, code int) error {
	failure := biliCodeFailure(code)
	if failure.Code == contract.CodeRateLimited {
		recordSubtitleRateLimit(client, nil)
	}
	return failure
}

func terminalSubtitleFailure(err error) bool {
	var application *contract.AppError
	if !errors.As(err, &application) {
		return false
	}
	switch application.Code {
	case contract.CodeAuthExpired, contract.CodeRateLimited, contract.CodeCancelled, contract.CodeTimeout:
		return true
	default:
		return false
	}
}
