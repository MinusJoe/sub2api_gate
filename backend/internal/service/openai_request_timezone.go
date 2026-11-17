package service

import (
	_ "embed"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	_ "time/tzdata"

	infraerrors "github.com/MACOS-DO/sub4api/internal/pkg/errors"
)

const DefaultOpenAIRequestTimezone = "Asia/Singapore"
const openAIRequestTimezoneExtraKey = "openai_request_timezone"

// openAIRequestTimezoneDefault 是进程级默认时区（gateway.openai_default_timezone /
// 出口区域预设给出的默认值），只给没有账号级 extra 的账号兜底。
// 未配置时等于内置常量 DefaultOpenAIRequestTimezone，即与改动前完全一致。
// 热路径只读，故用 atomic.Value 存字符串快照。
var openAIRequestTimezoneDefault = func() *atomic.Value {
	v := &atomic.Value{}
	v.Store(DefaultOpenAIRequestTimezone)
	return v
}()

// SetDefaultOpenAIRequestTimezone 发布进程级默认时区。
// 与账号级 extra 走同一份白名单判定：非法值（含空串）一律忽略并回落内置默认，
// 不允许把未经校验的字符串写进上游请求体。
func SetDefaultOpenAIRequestTimezone(tz string) {
	tz = strings.TrimSpace(tz)
	if !isAllowedOpenAIRequestTimezone(tz) {
		tz = DefaultOpenAIRequestTimezone
	}
	openAIRequestTimezoneDefault.Store(tz)
}

// DefaultOpenAIRequestTimezoneResolved 返回当前生效的进程级默认时区。
func DefaultOpenAIRequestTimezoneResolved() string {
	if tz, ok := openAIRequestTimezoneDefault.Load().(string); ok && tz != "" {
		return tz
	}
	return DefaultOpenAIRequestTimezone
}

//go:embed openai_request_timezones.txt
var openAIRequestTimezoneNames string

var openAIRequestTimezoneOptions = strings.Fields(openAIRequestTimezoneNames)
var openAIRequestTimezoneAllowed = func() map[string]struct{} {
	allowed := make(map[string]struct{}, len(openAIRequestTimezoneOptions))
	for _, name := range openAIRequestTimezoneOptions {
		allowed[name] = struct{}{}
	}
	return allowed
}()

func OpenAIRequestTimezoneOptions() []string {
	options := append([]string(nil), openAIRequestTimezoneOptions...)
	sort.Strings(options)
	return options
}

func ValidateOpenAIRequestTimezoneExtra(platform string, extra map[string]any) error {
	if extra == nil {
		return nil
	}
	if err := validateOpenAIRequestTimezoneModeExtra(platform, extra); err != nil {
		return err
	}
	raw, exists := extra[openAIRequestTimezoneExtraKey]
	if !exists || raw == nil {
		return nil
	}
	name, ok := raw.(string)
	if !ok || (name != "" && !isAllowedOpenAIRequestTimezone(name)) || (name != "" && platform != PlatformOpenAI) {
		return infraerrors.New(http.StatusBadRequest, "INVALID_OPENAI_REQUEST_TIMEZONE", "openai_request_timezone must be an allowed IANA timezone for an OpenAI account")
	}
	return nil
}

func isAllowedOpenAIRequestTimezone(name string) bool {
	if _, ok := openAIRequestTimezoneAllowed[name]; !ok {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// OpenAIRequestTimezone 返回该账号出站请求体应使用的 IANA 时区。
// 优先级：账号 extra 显式配置 > 进程级默认时区（gateway.openai_default_timezone /
// 出口区域预设） > 内置常量 DefaultOpenAIRequestTimezone。
func (account *Account) OpenAIRequestTimezone() string {
	if account != nil && account.IsOpenAI() {
		if name := account.getExtraString(openAIRequestTimezoneExtraKey); isAllowedOpenAIRequestTimezone(name) {
			return name
		}
	}
	return DefaultOpenAIRequestTimezoneResolved()
}
