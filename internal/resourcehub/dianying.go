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
)

type dianyingAdapter struct {
	mu       sync.Mutex
	cfg      SiteConfig
	http     *httpClient
	cache    string
	proxyURL string // 缓存上一次的代理地址，避免重复创建 Transport
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
		// 关闭代理或未配置代理地址时清除
		if a.proxyURL != "" {
			a.proxyURL = ""
			a.http.SetBaseTransport(nil)
		}
	}
}

func (a *dianyingAdapter) searchHeaders() map[string]string {
	a.mu.Lock()
	token := strings.TrimSpace(a.cfg.Token)
	cookie := strings.TrimSpace(a.cfg.Cookie)
	a.mu.Unlock()
	hdr := map[string]string{
		"Accept": "application/json,text/plain,*/*",
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
