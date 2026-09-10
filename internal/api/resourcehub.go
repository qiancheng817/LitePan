package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/resourcehub"
	"litepan/internal/settings"
)

// resourceHubConfig 是后台 resourcehub 配置的对外结构。
//
// 注意：这里把站点列表与每个站点的具体配置拆开，方便后台一次写全部，
// 也方便前台只关心"已启用且配置完整"的可用站点列表。
type resourceHubConfig struct {
	Enabled bool               `json:"enabled"`
	Sites   []string           `json:"sites"`      // 已启用的站点代号集合（写时同时回填到 KeyResourceHubSites）
	Options []resourcehub.SiteMeta `json:"options"` // 全部站点元信息（仅展示用，写入忽略）
	Framehdr resourceHubSiteConfig `json:"framehdr"`
	Jying    resourceHubSiteConfig `json:"jying"`
	Guanying resourceHubSiteConfig `json:"guanying"`
}

// resourceHubSiteConfig 单站点的对外配置（密码不回显，仅在 GET 时给出 password_configured 标记）。
//
// Cookie/Token/AppKey 这类次要字段以 Configured 布尔返回，长度不回显。
type resourceHubSiteConfig struct {
	URL                string `json:"url"`
	Username           string `json:"username"`
	PasswordConfigured bool   `json:"password_configured"`
	TokenConfigured    bool   `json:"token_configured"`
	CookieConfigured   bool   `json:"cookie_configured"`
	AppKeyConfigured   bool   `json:"app_key_configured"`
}

// getResourceHubConfig 返回当前 resourcehub 配置（不含敏感字段）。
func (h *Handler) getResourceHubConfig(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil || h.resourcehub == nil {
		writeOK(w, resourceHubConfig{Options: []resourcehub.SiteMeta{}})
		return
	}
	cfgs, enabled := resourcehub.LoadSites(h.settings)
	out := resourceHubConfig{
		Enabled: h.settings.Bool(settings.KeyResourceHubEnabled),
		Options: h.resourcehub.Sites(),
	}
	for code, on := range enabled {
		if on {
			out.Sites = append(out.Sites, code)
		}
	}
	// 站点顺序按预设，便于前端表单渲染
	out.Guanying = resourceHubSiteConfig{
		URL:                cfgs[resourcehub.SiteGuanying].BaseURL,
		Username:           cfgs[resourcehub.SiteGuanying].Username,
		PasswordConfigured: cfgs[resourcehub.SiteGuanying].Password != "",
		CookieConfigured:   cfgs[resourcehub.SiteGuanying].Token != "",
	}
	out.Jying = resourceHubSiteConfig{
		URL:                cfgs[resourcehub.SiteJying].BaseURL,
		Username:           cfgs[resourcehub.SiteJying].Username,
		PasswordConfigured: cfgs[resourcehub.SiteJying].Password != "",
		AppKeyConfigured:   cfgs[resourcehub.SiteJying].AppKey != "",
	}
	out.Framehdr = resourceHubSiteConfig{
		URL:                cfgs[resourcehub.SiteFramehdr].BaseURL,
		Username:           cfgs[resourcehub.SiteFramehdr].Username,
		PasswordConfigured: cfgs[resourcehub.SiteFramehdr].Password != "",
		TokenConfigured:    cfgs[resourcehub.SiteFramehdr].Token != "",
	}
	writeOK(w, out)
}

// updateResourceHubConfig 写入配置。密码/Cookie 仅在非空时覆盖（保留旧值）。
func (h *Handler) updateResourceHubConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled         bool                          `json:"enabled"`
		Sites           []string                      `json:"sites"`
		GuanyingURL     string                        `json:"guanying_url"`
		GuanyingUser    string                        `json:"guanying_username"`
		GuanyingPwd     string                        `json:"guanying_password"`
		GuanyingCookie  string                        `json:"guanying_cookie"`
		JyingURL        string                        `json:"jying_url"`
		JyingUser       string                        `json:"jying_username"`
		JyingPwd        string                        `json:"jying_password"`
		JyingAppKey      string                        `json:"jying_app_key"`
		FramehdrURL     string                        `json:"framehdr_url"`
		FramehdrUser    string                        `json:"framehdr_username"`
		FramehdrPwd     string                        `json:"framehdr_password"`
		FramehdrToken   string                        `json:"framehdr_token"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.settings == nil {
		writeErr(w, fmt.Errorf("settings 未就绪"))
		return
	}
	values := map[string]string{
		settings.KeyResourceHubEnabled:       strconv.FormatBool(in.Enabled),
		settings.KeyResourceHubGuanyingURL:   strings.TrimRight(strings.TrimSpace(in.GuanyingURL), "/"),
		settings.KeyResourceHubGuanyingUser:  strings.TrimSpace(in.GuanyingUser),
		settings.KeyResourceHubJyingURL:      strings.TrimRight(strings.TrimSpace(in.JyingURL), "/"),
		settings.KeyResourceHubJyingUser:     strings.TrimSpace(in.JyingUser),
		settings.KeyResourceHubFramehdrURL:   strings.TrimRight(strings.TrimSpace(in.FramehdrURL), "/"),
		settings.KeyResourceHubFramehdrUser:  strings.TrimSpace(in.FramehdrUser),
		settings.KeyResourceHubSites:         strings.Join(in.Sites, ","),
	}
	if in.GuanyingPwd != "" {
		values[settings.KeyResourceHubGuanyingPwd] = in.GuanyingPwd
	}
	if in.GuanyingCookie != "" {
		values[settings.KeyResourceHubGuanyingCookie] = in.GuanyingCookie
	}
	if in.JyingPwd != "" {
		values[settings.KeyResourceHubJyingPwd] = in.JyingPwd
	}
	if in.JyingAppKey != "" {
		values[settings.KeyResourceHubJyingAppKey] = in.JyingAppKey
	}
	if in.FramehdrPwd != "" {
		values[settings.KeyResourceHubFramehdrPwd] = in.FramehdrPwd
	}
	if in.FramehdrToken != "" {
		values[settings.KeyResourceHubFramehdrToken] = in.FramehdrToken
	}
	if err := h.settings.Update(r.Context(), values); err != nil {
		writeErr(w, err)
		return
	}
	// 刷新内存中的配置缓存
	if h.resourcehub != nil {
		h.resourcehub.Refresh()
	}
	h.getResourceHubConfig(w, r)
}

// searchResourceHub 公共 / 管理员搜索入口。
//
// 公共路径：必须先启用资源站；按 ?site=guanying,framehdr,... 限定站点；空表示全部启用站点。
// 管理员路径：可不启用资源站，单独试搜用于连通性测试；优先用 query string 中的临时配置。
func (h *Handler) searchResourceHub(w http.ResponseWriter, r *http.Request) {
	if h.resourcehub == nil {
		writeErr(w, fmt.Errorf("resourcehub 未初始化"))
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, fmt.Errorf("搜索关键词不能为空"))
		return
	}
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	sitesParam := r.URL.Query().Get("site")
	publicRequest := strings.HasPrefix(r.URL.Path, "/api/public/")
	if publicRequest && !h.resourcehub.Enabled() {
		writeErr(w, resourcehub.ErrDisabled)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), resourcehub.SearchTimeout+5_000_000_000) // +5s 缓冲
	defer cancel()

	if sitesParam == "all" || sitesParam == "" {
		// 全部：管理员走所有 adapter，公共只走 enabled+available
		results, failures := h.resourcehub.SearchAll(ctx, q, page)
		writeOK(w, map[string]any{
			"items":    flattenSearchAll(results),
			"groups":   results,
			"failures": failures,
			"q":        q,
			"page":     page,
		})
		return
	}
	// 限定站点
	codes := splitCSV(sitesParam)
	items := make(map[string][]resourcehub.Item, len(codes))
	failures := make([]string, 0)
	for _, code := range codes {
		list, err := h.resourcehub.Search(ctx, code, q, page)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", code, err.Error()))
			continue
		}
		items[code] = list
	}
	writeOK(w, map[string]any{
		"items":    flattenSearchAll(items),
		"groups":   items,
		"failures": failures,
		"q":        q,
		"page":     page,
	})
}

// flattenSearchAll 把 map[code]items 拍平为单个列表（带 Source 字段），便于前台统一渲染。
func flattenSearchAll(in map[string][]resourcehub.Item) []resourcehub.Item {
	if len(in) == 0 {
		return []resourcehub.Item{}
	}
	out := make([]resourcehub.Item, 0)
	for _, list := range in {
		out = append(out, list...)
	}
	return out
}

func splitCSV(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// searchResourceHubRaw 用于后台配置弹窗的连通性测试。
//
// 与 searchResourceHub 的差异：
//   - 不强制启用资源站总开关；
//   - 支持 endpoint 临时覆盖，便于测试自定义 URL；
//   - 失败时返回详细错误，UI 可直接展示。
//
// 凭据回退策略（关键）：前端表单里密码不回显，所以测试时 form.password 总是
// 空字符串。如果用户之前在表单里"填写过密码并点过保存"，那 settings 里
// 就有真实密码，应优先用 settings 里的值；只有当 settings 也没有凭据时，才
// 视为"未配置凭据"。
func (h *Handler) searchResourceHubRaw(w http.ResponseWriter, r *http.Request) {
	if h.resourcehub == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "resourcehub 未初始化"))
		return
	}
	var in struct {
		Site    string `json:"site"`
		Q       string `json:"q"`
		URL     string `json:"url"`
		User    string `json:"username"`
		Pwd     string `json:"password"`
		Token   string `json:"token"`
		Cookie  string `json:"cookie"`
		AppKey  string `json:"app_key"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if in.Site == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "site 不能为空"))
		return
	}
	adapter, ok := h.resourcehub.GetAdapter(in.Site)
	if !ok {
		writeErr(w, domain.Errorf(domain.CodeValidation, "未知站点：%s", in.Site))
		return
	}
	// 1) 先从 settings 加载完整配置（凭据就在这里）
	cfg := resourcehub.SiteConfig{
		Code:    in.Site,
		BaseURL: "",
	}
	if h.settings != nil {
		if cfgs, _ := resourcehub.LoadSites(h.settings); cfgs != nil {
			if c, ok := cfgs[in.Site]; ok {
				cfg = c
			}
		}
	}
	// 2) 前端传的字段如果非空，用前端值覆盖（便于"未保存的临时配置"也能立即测试）
	if url := strings.TrimRight(strings.TrimSpace(in.URL), "/"); url != "" {
		cfg.BaseURL = url
	}
	if user := strings.TrimSpace(in.User); user != "" {
		cfg.Username = user
	}
	if in.Pwd != "" {
		cfg.Password = in.Pwd
	}
	if in.Token != "" {
		cfg.Token = in.Token
	}
	if in.Site == resourcehub.SiteGuanying && in.Cookie != "" {
		cfg.Token = in.Cookie
	}
	if in.Site == resourcehub.SiteJying && in.AppKey != "" {
		cfg.AppKey = in.AppKey
	}
	if cfg.BaseURL == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "站点地址不能为空，请先在表单填写站点地址"))
		return
	}
	adapter.SetConfig(cfg)
	ctx, cancel := context.WithTimeout(r.Context(), resourcehub.SearchTimeout+5_000_000_000)
	defer cancel()
	items, err := adapter.Search(ctx, strings.TrimSpace(in.Q), 1)
	if err != nil {
		// 把"未配置凭据"这种最常见的可预期错误转换成对用户更直观的文案。
		// 适配器仅返回 sentinel error，HTTP 层负责翻译。
		if errors.Is(err, resourcehub.ErrAuthRequired) {
			code := siteDisplayName(in.Site)
			writeErr(w, domain.Errorf(domain.CodeValidation,
				"%s尚未配置登录凭据（账号或密码为空）。请在表单中填写后点保存再测试（密码字段会从 settings 自动加载，未保存时为空属正常）",
				code))
			return
		}
		if errors.Is(err, resourcehub.ErrAuthFailed) {
			code := siteDisplayName(in.Site)
			writeErr(w, domain.Errorf(domain.CodeValidation,
				"%s登录失败，请检查 Token / Cookie 是否有效或已过期。若使用 Cookie，请粘贴完整的浏览器 Cookie 后保存再测试。",
				code))
			return
		}
		code := siteDisplayName(in.Site)
		writeErr(w, domain.Errorf(domain.CodeValidation, "%s连通测试失败：%s", code, err.Error()))
		return
	}
	preview := items
	if len(preview) > 5 {
		preview = preview[:5]
	}
	writeOK(w, map[string]any{
		"count":   len(items),
		"preview": preview,
	})
}

// 占位避免 import unused
var _ = json.RawMessage{}

// siteDisplayName 把站点代号转成中文名（用于错误提示）。
func siteDisplayName(code string) string {
	switch code {
	case resourcehub.SiteGuanying:
		return "观影"
	case resourcehub.SiteJying:
		return "聚影"
	case resourcehub.SiteFramehdr:
		return "帧影"
	default:
		return code
	}
}