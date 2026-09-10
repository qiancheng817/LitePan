package resourcehub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// User-Agent 常量
const (
	dianyingUAChrome124 = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	dianyingUAEdge131   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0"
)

// Cookie 保活间隔
const cookieKeepaliveInterval = 6 * time.Hour

// UA 选择常量
const (
	UAChoiceChrome = "chrome124"
	UAChoiceEdge   = "edge131"
)

type dianyingAdapter struct {
	mu            sync.Mutex
	cfg           SiteConfig
	http          *httpClient
	cache         string
	proxyURL      string
	lastKeepalive time.Time
}

func NewDianyingAdapter() Adapter {
	return &dianyingAdapter{http: newHTTPClient(SearchTimeout)}
}

func (a *dianyingAdapter) Code() string { return SiteDianying }

func (a *dianyingAdapter) Meta() SiteMeta {
	return SiteMeta{
		Code:        SiteDianying,
		Name:        "癫影",
		BaseURL:     "https://m.dian115.com",
		Description: "癫影 · DIAN-115 OpenAPI 资源检索；需 VIP 创建 API Key，返回 115 网盘分享链接。",
		NeedsAuth:   true,
	}
}

func (a *dianyingAdapter) SetConfig(cfg SiteConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cache != cfg.Token {
		a.http.ResetCookies()
		a.cache = cfg.Token
	}
	a.cfg = cfg
	// 代理配置变化时更新 HTTP Transport
	if cfg.UseProxy {
		if cfg.ProxyURL != "" && cfg.ProxyURL != a.proxyURL {
			a.proxyURL = cfg.ProxyURL
			parsed, err := url.Parse(cfg.ProxyURL)
			if err == nil {
				a.http.SetBaseTransport(&http.Transport{
					Proxy: http.ProxyURL(parsed),
				})
			}
		}
	} else {
		if a.proxyURL != "" {
			a.proxyURL = ""
			a.http.SetBaseTransport(nil)
		}
	}
}

// resolveUA 根据配置返回对应的 User-Agent 字符串。
func (a *dianyingAdapter) resolveUA() string {
	switch a.cfg.UAChoice {
	case UAChoiceEdge:
		return dianyingUAEdge131
	default:
		return dianyingUAChrome124
	}
}

// searchHeaders 构建请求头，使用配置选择的 User-Agent。
func (a *dianyingAdapter) searchHeaders() map[string]string {
	a.mu.Lock()
	token := strings.TrimSpace(a.cfg.Token)
	cookie := strings.TrimSpace(a.cfg.Cookie)
	ua := a.resolveUA()
	a.mu.Unlock()
	hdr := map[string]string{
		"User-Agent":      ua,
		"Accept":          "application/json,text/plain,*/*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
		"Referer":         "https://m.dian115.com/",
		"Origin":          "https://m.dian115.com",
	}
	if token != "" {
		hdr["Authorization"] = "Bearer " + token
		hdr["X-API-Key"] = token
	}
	if cookie != "" {
		hdr["Cookie"] = cookie
	}
	return hdr
}

// keepaliveCheck 检查是否需要执行 Cookie 保活（仅在 Keepalive 开启时生效）。
func (a *dianyingAdapter) keepaliveCheck(ctx context.Context) {
	a.mu.Lock()
	keepaliveEnabled := a.cfg.Keepalive
	cookie := strings.TrimSpace(a.cfg.Cookie)
	baseURL := strings.TrimSpace(a.cfg.BaseURL)
	sinceLast := time.Since(a.lastKeepalive)
	a.mu.Unlock()

	// 保活未开启 或 没有 Cookie 则跳过
	if !keepaliveEnabled || cookie == "" || baseURL == "" {
		return
	}

	// 未到保活间隔则跳过
	if sinceLast < cookieKeepaliveInterval {
		return
	}

	// 发起轻量保活请求
	keepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = a.http.GetWithCookies(keepCtx, baseURL+"/", a.searchHeaders(), "")

	a.mu.Lock()
	a.lastKeepalive = time.Now()
	a.mu.Unlock()
}

func (a *dianyingAdapter) Search(ctx context.Context, q string, page int) ([]Item, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	if !IsConfigured(cfg) {
		return nil, errors.New("癫影站地址未配置")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("癫影搜索需要 OpenAPI Token，请在「OpenAPI Token」字段填写 API Key 后保存再测试（Cookie 仅作为辅助登录，不能替代 Token）")
	}
	if !HasAuth(cfg) {
		return nil, ErrAuthRequired
	}

	// 执行 Cookie 保活检查（仅当开关开启时才实际执行）
	a.keepaliveCheck(ctx)

	u, _ := url.Parse(cfg.BaseURL + "/api/open/search")
	q2 := u.Query()
	q2.Set("q", q)
	if page > 1 {
		q2.Set("page", fmt.Sprintf("%d", page))
	}
	u.RawQuery = q2.Encode()
	body, err := a.http.GetWithCookies(ctx, u.String(), a.searchHeaders(), "")
	if err != nil {
		if code, ok := IsStatusError(err); ok && (code == 401 || code == 403) {
			return nil, ErrAuthFailed
		}
		if code, ok := IsStatusError(err); ok {
			return nil, fmt.Errorf("癫影搜索返回 HTTP %d，请检查站点地址和 API 路径是否正确", code)
		}
		return nil, fmt.Errorf("癫影搜索请求失败：%w", err)
	}
	items, err := parseDianyingSearch(body, cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Source = SiteDianying
		if items[i].Code == "" {
			items[i].Code = SiteDianying
		}
		if plat := detectPanFromURL(items[i].URL); plat != "" {
			items[i].Platform = plat
		} else if items[i].Platform == "" {
			items[i].Platform = "癫影"
		}
	}
	return items, nil
}

func parseDianyingSearch(body, baseURL string) ([]Item, error) {
	var resp struct {
		Data    []json.RawMessage `json:"data"`
		Results []json.RawMessage `json:"results"`
		Items   []json.RawMessage `json:"items"`
		List    []json.RawMessage `json:"list"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("癫影响应不是合法 JSON：%w", err)
	}
	rawList := resp.Data
	if len(rawList) == 0 {
		rawList = resp.Results
	}
	if len(rawList) == 0 {
		rawList = resp.Items
	}
	if len(rawList) == 0 {
		rawList = resp.List
	}
	out := make([]Item, 0, len(rawList))
	for _, raw := range rawList {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		title := asString(m["title"])
		if title == "" {
			title = asString(m["name"])
		}
		if title == "" {
			continue
		}
		itemURL := asString(m["url"])
		if itemURL == "" {
			itemURL = asString(m["link"])
		}
		if itemURL != "" && strings.HasPrefix(itemURL, "/") {
			itemURL = strings.TrimRight(baseURL, "/") + itemURL
		}
		pwd := asString(m["password"])
		if pwd == "" {
			pwd = asString(m["pwd"])
		}
		if pwd == "" {
			pwd = asString(m["extract_code"])
		}
		var tags []string
		if v := asString(m["type"]); v != "" {
			tags = append(tags, v)
		}
		if y := asInt(m["year"]); y > 0 {
			tags = append(tags, fmt.Sprintf("%d", y))
		}
		out = append(out, Item{
			Title:    title,
			URL:      itemURL,
			Password: pwd,
			Tags:     tags,
		})
	}
	if len(out) == 0 && len(body) > 0 {
		return nil, ErrEmpty
	}
	return out, nil
}
