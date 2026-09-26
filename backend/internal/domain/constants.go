// Package domain 存放跨层共享的枚举和常量。
package domain

// 状态常量。
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusError    = "error"
)

// 角色常量。cmd2api 仅管理员模式，注册入口关闭，user 角色只为将来扩展保留。
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// 上游平台。
//
// cmd2api 支持两种上游，协议形态差别很大：
//
//	commandcode —— 自有协议。要把请求包成信封、发 NDJSON 流、按设备指纹伪装身份。
//	opencode    —— OpenAI 兼容协议。基本是直通，把 OpenAI 请求转过去即可。
//
// 分组绑定平台（见 Group.platform），请求按分组路由到对应平台的账号池，
// 避免「某个模型只在一个平台上有，却被路由到另一个平台」。
const (
	PlatformCommandCode = "commandcode"
	PlatformOpenCode    = "opencode"
)

// 账号认证类型。两种平台目前都只用 API Key。
const (
	AccountTypeAPIKey = "apikey"
)

// CommandCodeKeyPrefix 是 Command Code 密钥的前缀。
// 只对 commandcode 平台做这个校验——OpenCode 的密钥格式不同。
const CommandCodeKeyPrefix = "user_"

// OpenCode 的两种计费模式，存在账号的 extra.account_mode 里。
//
// 两者只是 base_url 不同，协议与鉴权一样；分开是为了让后台能按模式分组展示，
// 也方便将来针对订阅额度做窗口监控。
const (
	// AccountModeZen 是按量付费：https://opencode.ai/zen/v1
	AccountModeZen = "zen"
	// AccountModeGo 是订阅额度：https://opencode.ai/zen/go/v1
	AccountModeGo = "go"
)

// OpenCodeBaseURLZen / OpenCodeBaseURLGo 是两种模式的默认上游地址。
//
// 账号里可以覆盖（存 extra.base_url），适用于自建中转或私有部署的场景。
const (
	OpenCodeBaseURLZen = "https://opencode.ai/zen/v1"
	OpenCodeBaseURLGo  = "https://opencode.ai/zen/go/v1"
)

// DefaultOpenCodeBaseURL 按计费模式返回默认上游地址。
func DefaultOpenCodeBaseURL(accountMode string) string {
	if accountMode == AccountModeGo {
		return OpenCodeBaseURLGo
	}
	return OpenCodeBaseURLZen
}

// IsValidPlatform 判断平台取值是否受支持。
func IsValidPlatform(p string) bool {
	return p == PlatformCommandCode || p == PlatformOpenCode
}

// APIKeyPrefix 是签发给下游客户端的密钥前缀，便于在日志里一眼认出来源。
const APIKeyPrefix = "sk-c2a-"

// UsageLog 里 billing_mode 的取值。
const (
	BillingModeToken = "token"
)
