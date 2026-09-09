package resourcehub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

const (
	httpUnauthorized = 401
	httpForbidden    = 403
)

// jyingAdapter 聚影 (https://www.jying.top) 适配器。
//
// 聚影的鉴权模型与 Django REST 框架一致：
//
//   - 登录：POST /api/app/login/ （JSON Body {username, password}）
//     返回 {status, message, token}。token 是 Django 的 session token（CSRF
//     token 已不再要求：实测 GET /api/csrf/ 在 2026 年已返回 {"status":"success"}
//     但 Set-Cookie 为空，且 POST 不带 csrftoken 也能正常登录）。
//   - 后续接口：Header `Authorization: Token <token>` +
//     `X-Requested-With: XMLHttpRequest`。
//   - 搜索：GET /api/app/movies/?q=<kw>&page=<n> → 返回
//     {status, page, page_size, total_pages, total_count, count, has_more,
//     results:[{id, title, cover, year, movie_type_display, douban_rating,
//     tag_names, ...}]}。
//
// URL 字段：Item.URL = {BaseURL}/movie/{id}（SPA 详情页路由）。用户从后台复制
// 链接后可以在浏览器里打开，手动选择网盘分享。
type jyingAdapter struct {
	mu     sync.Mutex
	cfg    SiteConfig
	http   *httpClient
	token  string
	cached string // 上一次 username，用于检测配置变更时强制重新登录
}

func NewJyingAdapter() Adapter {
	return &jyingAdapter{http: newHTTPClient(SearchTimeout)}
}

func (a *jyingAdapter) Code() string { return SiteJying }

func (a *jyingAdapter) Meta() SiteMeta {
	return SiteMeta{
		Code:        SiteJying,
		Name:        "聚影",
		BaseURL:     "https://www.jying.top",
		Description: "聚影 · 网盘资源收集者；需账号登录（POST /api/app/login/ 返回 Token），登录后 Token 一直有效。",
		NeedsAuth:   true,
	}
}

func (a *jyingAdapter) SetConfig(cfg SiteConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cached != cfg.Username {
		a.token = ""
		a.http.ResetCookies()
	}
	a.cfg = cfg
}

// ensureLogin 已登录则跳过；否则尝试一次。
func (a *jyingAdapter) ensureLogin(ctx context.Context) error {
	a.mu.Lock()
	if a.token != "" {
		a.mu.Unlock()
		return nil
	}
	if !HasAuth(a.cfg) {
		a.mu.Unlock()
		return ErrAuthRequired
	}
	cfg := a.cfg
	a.mu.Unlock()

	loginBody, _ := json.Marshal(map[string]string{
		"username": cfg.Username,
		"password": cfg.Password,
	})
	loginHeaders := map[string]string{
		"X-Requested-With": "XMLHttpRequest",
	}
	if k := strings.TrimSpace(cfg.AppKey); k != "" {
		loginHeaders["App-Key"] = k
	}
	resp, status, err := a.http.PostJSON(ctx, cfg.BaseURL+"/api/app/login/", loginBody, loginHeaders, "")
	if err != nil {
		return fmt.Errorf("聚影登录请求失败：%w", err)
	}
	if status == httpUnauthorized {
		return ErrAuthFailed
	}
	if status >= 500 {
		return fmt.Errorf("聚影登录上游异常（HTTP %d）", status)
	}

	var respJSON struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal([]byte(resp), &respJSON); err != nil {
		return fmt.Errorf("聚影登录响应解析失败：%w", err)
	}
	if respJSON.Status != "success" || respJSON.Token == "" {
		return fmt.Errorf("%w：%s", ErrAuthFailed, trimJyingErr(respJSON.Message, resp))
	}

	a.mu.Lock()
	a.token = respJSON.Token
	a.cached = cfg.Username
	a.mu.Unlock()
	return nil
}

// searchHeaders 构造每次搜索请求的通用 header。
//   - Authorization: Token <jwt>（聚影自家鉴权）
//   - App-Key: 可选（部分上游校验，cfg.AppKey 非空时附加）
func (a *jyingAdapter) searchHeaders() map[string]string {
	a.mu.Lock()
	token := a.token
	appKey := strings.TrimSpace(a.cfg.AppKey)
	a.mu.Unlock()
	hdr := map[string]string{
		"Authorization":    "Token " + token,
		"X-Requested-With": "XMLHttpRequest",
		"Accept":           "application/json,text/plain,*/*",
	}
	if appKey != "" {
		hdr["App-Key"] = appKey
	}
	return hdr
}

// trimJyingErr 摘取错误描述，trim 长度供上层使用。
func trimJyingErr(primary, body string) string {
	if primary != "" {
		return primary
	}
	s := strings.TrimSpace(body)
	const max = 200
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func (a *jyingAdapter) Search(ctx context.Context, q string, page int) ([]Item, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if !IsConfigured(cfg) {
		return nil, errors.New("聚影站地址未配置")
	}
	if !HasAuth(cfg) {
		return nil, ErrAuthRequired
	}
	if err := a.ensureLogin(ctx); err != nil {
		return nil, err
	}

	u, _ := url.Parse(cfg.BaseURL + "/api/app/movies/")
	q2 := u.Query()
	q2.Set("q", q)
	q2.Set("page", fmt.Sprintf("%d", page))
	q2.Set("sort", "")
	q2.Set("order", "")
	u.RawQuery = q2.Encode()

	body, err := a.http.GetWithCookies(ctx, u.String(), a.searchHeaders(), "")
	if err != nil {
		// 401 / 403 视为登录失效，清 token 强制下次重登
		if code, ok := IsStatusError(err); ok && (code == httpUnauthorized || code == httpForbidden) {
			a.mu.Lock()
			a.token = ""
			a.mu.Unlock()
			return nil, ErrAuthRequired
		}
		return nil, fmt.Errorf("聚影搜索失败：%w", err)
	}
	items, err := parseJyingMovies(body, cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Source = SiteJying
		if items[i].Code == "" {
			items[i].Code = SiteJying
		}
		// 如果 URL 命中已知网盘域名，把 Platform 改成对应网盘名，
		// 这样前台会显示对应颜色的标签 + 转存按钮。
		if plat := detectPanFromURL(items[i].URL); plat != "" {
			items[i].Platform = plat
		} else if items[i].Platform == "" {
			items[i].Platform = "聚影"
		}
	}
	return items, nil
}

// parseJyingMovies 解析聚影 /api/app/movies/ 响应。
//
// 输入是 {status, page, ..., results: [{id, title, ...}, ...]}，对每个结果：
//   - Title  = title
//   - URL    = {BaseURL}/movie/{id}（SPA 路由，用户手动在浏览器打开）
//   - Tags   = tag_names + year（作为"年份"标签） + movie_type_display
func parseJyingMovies(body, baseURL string) ([]Item, error) {
	var resp struct {
		Status  string            `json:"status"`
		Results []json.RawMessage `json:"results"`
		Items   []json.RawMessage `json:"items"`
		Movies  []json.RawMessage `json:"movies"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("聚影响应不是合法 JSON：%w", err)
	}
	rawList := resp.Results
	if len(rawList) == 0 {
		rawList = resp.Items
	}
	if len(rawList) == 0 {
		rawList = resp.Movies
	}
	out := make([]Item, 0, len(rawList))
	for _, raw := range rawList {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		id := asString(m["id"])
		if id == "" {
			continue
		}
		title := asString(m["title"])
		if title == "" {
			continue
		}
		out = append(out, Item{
			Title: title,
			URL:   strings.TrimRight(baseURL, "/") + "/movie/" + id,
			Tags:  jyingTags(m),
		})
	}
	if len(out) == 0 && len(body) > 0 {
		// 协议变更时返回空，提醒调用方
		return nil, ErrEmpty
	}
	return out, nil
}

// jyingTags 提取聚影条目的标签：类型 + 年份 + tag_names + 豆瓣评分。
func jyingTags(m map[string]any) []string {
	var tags []string
	if v, ok := m["movie_type_display"].(string); ok && v != "" {
		tags = append(tags, v)
	}
	if y := asInt(m["year"]); y > 0 {
		tags = append(tags, fmt.Sprintf("%d", y))
	}
	if names, ok := m["tag_names"].([]any); ok {
		for _, n := range names {
			if s, ok := n.(string); ok && s != "" {
				tags = append(tags, s)
			}
		}
	}
	if r := asFloat(m["douban_rating"]); r > 0 {
		tags = append(tags, fmt.Sprintf("豆瓣%.1f", r))
	}
	return tags
}

// asString 容忍 JSON 数字/字符串统一转换为字符串。
func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%v", uint64(x))
	case json.Number:
		return x.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

// asInt 把 JSON number 强转 int。
func asInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	case int:
		return x
	}
	return 0
}

func asFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case int:
		return float64(x)
	}
	return 0
}
