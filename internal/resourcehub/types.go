// Package resourcehub 聚合外部影视资源分享站（观影/聚影/帧影），
// 在 LitePan 前台首页提供统一的「资源搜索 + 一键转存」入口。
//
// 设计要点：
//   - 每个站点都是独立的 Adapter：实现 Search 接口返回统一格式的搜索结果，
//     供前台展示与一键转存复用 PanSouSaveModal 流程。
//   - 登录态、Cookie、CSRF token 等敏感状态仅缓存在内存，进程退出即失效。
//   - 适配器允许匿名调用（无账号密码也能返回部分结果），登录态仅用于解锁
//     需鉴权的接口（如聚影的搜索）。失败以明确的错误信息返回，方便前端
//     在配置弹窗里给出针对性提示。
package resourcehub

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Site 站点代号常量，与前端 resourcehub_sites 字符串一致。
const (
	SiteGuanying = "guanying" // 观影 xn--wcv59z.com
	SiteJying    = "jying"    // 聚影 jying.top
	SiteFramehdr = "framehdr" // 帧影 framehdr.com
)

// 所有支持的站点代号，供前端校验。
var AllSites = []string{SiteGuanying, SiteJying, SiteFramehdr}

// SiteMeta 站点展示元数据。
type SiteMeta struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	Description string `json:"description"`
	NeedsAuth   bool   `json:"needs_auth"`
	Available   bool   `json:"available"` // 当前是否可用：基础架构 + 设置完整
	Note        string `json:"note,omitempty"`
}

// SiteConfig 单站点可保存的完整配置（从 settings 装配）。
//
// 字段含义：
//   - Code/BaseURL/Username/Password：站点登录凭据。
//   - Token：复用字段，不同站点含义不同：
//       - 帧影：登录后的浏览器 Cookie 串（用于绕过 GeeTest 验证）。
//       - 观影：可直接粘贴整段 Cookie（`browser_verified=xxx; app_auth=xxx; ...`），
//         设置后适配器会跳过 PoW 与表单登录，直接复用这组 Cookie。
//   - AppKey：聚影站点专用，置于 HTTP 请求头 `App-Key`（实测部分上游会校验）。
type SiteConfig struct {
	Code      string
	BaseURL   string
	Username  string
	Password  string
	Token     string
	Cookie    string
	AppKey    string
}

// Enabled 表示当前用户是否启用某个站点。
func SetEnabled(raw string) map[string]bool {
	out := make(map[string]bool, len(AllSites))
	for _, code := range AllSites {
		out[code] = false
	}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == ' ' || r == '\t'
	}) {
		part = strings.ToLower(strings.TrimSpace(part))
		if _, ok := out[part]; ok {
			out[part] = true
		}
	}
	return out
}

// Item 通用搜索结果条目，供前台统一渲染（与 PanSouItem 字段集对齐，方便复用）。
type Item struct {
	Code     string   `json:"code"`               // PanSouItem.code 兼容：站点代号或网盘代号
	Platform string   `json:"platform"`           // 显示名：站点名 / 网盘名
	Title    string   `json:"title"`              // 资源标题
	URL      string   `json:"url"`                // 分享链接或磁链
	Password string   `json:"password,omitempty"` // 提取码
	Tags     []string `json:"tags,omitempty"`     // 类型、年份、地区等元信息
	// Source 标记来源站点，便于前台做来源筛选。
	Source string `json:"source"`
}

// Adapter 单个资源站适配器接口。
type Adapter interface {
	// Code 返回站点代号。
	Code() string
	// Meta 返回站点展示元信息（BaseURL/Description/NeedsAuth 等）。
	Meta() SiteMeta
	// SetConfig 注入最新配置（每次 Search 前由 Service 调用）。
	// 适配器应自行处理登录态刷新（如账号密码变了即丢弃旧 token）。
	SetConfig(cfg SiteConfig)
	// Search 在该站点上搜索关键词。ctx 透传便于超时控制。
	Search(ctx context.Context, q string, page int) ([]Item, error)
}

// Errors 包级常用错误，方便前端识别处理。
var (
	ErrDisabled     = errors.New("资源站未启用")
	ErrAuthRequired = errors.New("该站点搜索需要登录")
	ErrAuthFailed   = errors.New("资源站登录失败")
	ErrRateLimited  = errors.New("资源站访问被限流，请稍后再试")
	ErrEmpty        = errors.New("资源站返回为空")
)

// PageSize 单页条数上限，前台分页统一。
const PageSize = 24

// SearchTimeout 单次搜索总超时（毫秒），避免上游挂起时整个面板卡住。
const SearchTimeout = 15 * time.Second