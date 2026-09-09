package resourcehub

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

// framehdrAdapter 帧影 (https://framehdr.com) 适配器。
//
// 帧影为 PHP 服务端渲染，匿名可搜索（返回 HTML 卡片列表）；登录需 GeeTest
// 行为验证码，自动化难度大，故本适配器支持两种使用方式：
//
//  1. 匿名：直接 GET /search.php?q=...，解析 .resource-card 列表。
//  2. 半登录：管理员可在配置里填写 Token（已登录后的整段 Cookie 字符串）。
//     LitePan 会原样附加到 Search/Detail 请求中，用于解锁会员内容。
//
// 每条 Item 返回时，URL 指向 /detail.php?id=N；可选地，由 Search 内部并发
// 拉取详情页（限速 4 并发），用正则提取 115 / 光鸭云盘 / 磁链 / ed2k 等真实
// 分享链接替换 item.URL。这样前台点"复制链接"拿到的是可以直接打开的资源链接。
type framehdrAdapter struct {
	mu   sync.Mutex
	cfg  SiteConfig
	http *httpClient

	// 控制搜索时是否自动钻取详情（慢但能拿到真实网盘链接）
	enrichItems bool
}

// 默认开启详情钻取；可在 SetConfig 通过 Token 带 "+no_enrich" 后缀关闭。
// 例：PHPSESSID=xxx; +no_enrich
const framehdrEnrichOffTag = "+no_enrich"

func NewFramehdrAdapter() Adapter {
	return &framehdrAdapter{
		http:        newHTTPClient(SearchTimeout),
		enrichItems: true,
	}
}

func (a *framehdrAdapter) Code() string { return SiteFramehdr }

func (a *framehdrAdapter) Meta() SiteMeta {
	return SiteMeta{
		Code:        SiteFramehdr,
		Name:        "帧影",
		BaseURL:     "https://framehdr.com",
		Description: "帧影 · 115 网盘 + 光鸭云盘 + 磁链/ed2k；匿名可搜索，登录需 GeeTest 验证（半登录可设 Cookie）。",
		NeedsAuth:   false,
	}
}

func (a *framehdrAdapter) SetConfig(cfg SiteConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Username != cfg.Username || a.cfg.Password != cfg.Password || a.cfg.Token != cfg.Token {
		a.http.ResetCookies()
		a.enrichItems = !strings.Contains(cfg.Token, framehdrEnrichOffTag)
	}
	a.cfg = cfg
}

// Search 流程：GET /search.php?q=<kw> → 解析 HTML 卡片 → 可选并发钻取详情拿真实网盘链接。
func (a *framehdrAdapter) Search(ctx context.Context, q string, page int) ([]Item, error) {
	a.mu.Lock()
	cfg := a.cfg
	enrich := a.enrichItems
	a.mu.Unlock()
	if !IsConfigured(cfg) {
		return nil, errors.New("帧影站地址未配置")
	}

	// 分页（网站实际是 ?page=N）
	target := cfg.BaseURL + "/search.php"
	qstr := url.Values{}
	qstr.Set("q", q)
	if page > 1 {
		qstr.Set("page", strconv.Itoa(page))
	}
	// 用户的 raw Cookie，去掉 +no_enrich 控制后缀
	rawCookie := strings.TrimSpace(strings.ReplaceAll(cfg.Token, framehdrEnrichOffTag, ""))
	body, err := a.http.GetWithCookies(ctx, target+"?"+qstr.Encode(), nil, rawCookie)
	if err != nil {
		return nil, fmt.Errorf("帧影搜索请求失败：%w", err)
	}

	items, err := parseFramehdrSearchHTML(body, cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	// 详情钻取：拿到真实网盘链接，覆盖 URL 字段。
	if enrich && len(items) > 0 {
		a.enrichItemsWithDetails(ctx, items, rawCookie)
	}
	// 详情钻取完后，统一设置字段；若 URL 命中已知网盘域名则 Platform 改成对应网盘名。
	for i := range items {
		items[i].Source = SiteFramehdr
		if items[i].Code == "" {
			items[i].Code = SiteFramehdr
		}
		if plat := detectPanFromURL(items[i].URL); plat != "" {
			items[i].Platform = plat
		} else if items[i].Platform == "" {
			items[i].Platform = "帧影"
		}
	}
	return items, nil
}

// enrichItemsWithDetails 并发钻取每条 Item 的 /detail.php?id=N，用 pan 链接覆盖 URL。
// 并发上限 4，避免压垮目标站。
func (a *framehdrAdapter) enrichItemsWithDetails(ctx context.Context, items []Item, rawCookie string) {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range items {
		if items[i].URL == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			link, platform := a.fetchFirstPanLink(ctx, items[i].URL, rawCookie)
			if link != "" {
				items[i].URL = link
				if platform != "" {
					items[i].Tags = append(items[i].Tags, "网盘:"+platform)
				}
			}
		}(i)
	}
	wg.Wait()
}

// fetchFirstPanLink 拉详情页取第一条 pan 链接 + 平台名（用于打标）。
func (a *framehdrAdapter) fetchFirstPanLink(ctx context.Context, detailURL, rawCookie string) (string, string) {
	body, err := a.http.GetWithCookies(ctx, detailURL, nil, rawCookie)
	if err != nil {
		return "", ""
	}
	links := extractPanLinks(body)
	if len(links) == 0 {
		return "", ""
	}
	for _, link := range links {
		for _, p := range panLinkPatterns {
			if p.pat.MatchString(link) {
				return link, p.tag
			}
		}
		return link, "网盘"
	}
	return "", ""
}

// fetchDetailPanLinks 拉详情页，用正则提取真实分享链接。
func (a *framehdrAdapter) fetchDetailPanLinks(ctx context.Context, detailURL, rawCookie string) []string {
	body, err := a.http.GetWithCookies(ctx, detailURL, nil, rawCookie)
	if err != nil {
		return nil
	}
	return extractPanLinks(body)
}

// extractPanLinks 从帧影详情页 HTML 抽取 115/光鸭云盘/磁链/ed2k 链接。
var panLinkPatterns = []struct {
	pat *regexp.Regexp
	tag string
}{
	{regexp.MustCompile(`https?://(?:115cdn\.com|115\.com|114\.115\.com)/s/[a-zA-Z0-9_-]+`), "115"},
	{regexp.MustCompile(`https?://(?:www\.)?guangyapan\.com/s/[a-zA-Z0-9_-]+`), "光鸭云盘"},
	{regexp.MustCompile(`https?://pan\.quark\.cn/s/[a-zA-Z0-9]+`), "夸克"},
	{regexp.MustCompile(`magnet:\?xt=urn:btih:[a-zA-Z0-9]+`), "磁链"},
	{regexp.MustCompile(`ed2k://\|\w+\|[^|]+`), "电驴"},
}

func extractPanLinks(body string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 4)
	for _, p := range panLinkPatterns {
		for _, m := range p.pat.FindAllString(body, -1) {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	if len(out) == 0 {
		// 退化：兜底查找 /s/xxxxxxx 形态
		for _, m := range regexp.MustCompile(`https?://[\w.-]+/s/[a-zA-Z0-9_-]{8,}`).FindAllString(body, -1) {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// parseFramehdrSearchHTML 解析帧影搜索结果 HTML 列表。
func parseFramehdrSearchHTML(body string, baseURL string) ([]Item, error) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解析帧影搜索页失败：%w", err)
	}
	items := make([]Item, 0, 24)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && hasClass(n, "resource-card") {
			if item, ok := extractFramehdrCard(n, baseURL); ok {
				items = append(items, item)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(items) == 0 {
		// 网站是 0 结果就保持空，调用方会显示提示
		return items, nil
	}
	return items, nil
}

// extractFramehdrCard 从单个 .resource-card 节点抽取 ID/标题/分类/评分。
func extractFramehdrCard(n *html.Node, baseURL string) (Item, bool) {
	item := Item{}
	id := findFramehdrCardID(n)
	if id == "" {
		return item, false
	}
	item.URL = baseURL + "/detail.php?id=" + id

	// 标题：明确从 <h3 class="card-title">...</h3>
	walkAttrElement(n, "h3", "card-title", func(text string) {
		t := strings.TrimSpace(text)
		if t != "" {
			item.Title = t
		}
	})
	if item.Title == "" {
		return Item{}, false
	}

	// 分类：<span class="category-badge-overlay">电影 / 剧集 / ...</span>
	walkAttrElement(n, "span", "category-badge-overlay", func(text string) {
		t := strings.TrimSpace(text)
		if t != "" {
			item.Tags = append(item.Tags, t)
		}
	})

	// 评分：<span class="rating-badge">4.6</span> 或 "暂无评分"
	walkAttrElement(n, "span", "rating-badge", func(text string) {
		t := strings.TrimSpace(text)
		if t != "" {
			item.Tags = append(item.Tags, "★"+t)
		}
	})
	return item, true
}

// findFramehdrCardID 优先 onclick="detail.php?id=N"，其次 a/href。
var detailIDRe = regexp.MustCompile(`detail\.php\?id=(\d+)`)

func findFramehdrCardID(n *html.Node) string {
	var find func(*html.Node) string
	find = func(node *html.Node) string {
		// 查 onclick 属性
		for _, a := range node.Attr {
			if a.Key == "onclick" {
				if m := detailIDRe.FindStringSubmatch(a.Val); len(m) >= 2 {
					return m[1]
				}
			}
			if a.Key == "href" {
				if m := detailIDRe.FindStringSubmatch(a.Val); len(m) >= 2 {
					return m[1]
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if id := find(c); id != "" {
				return id
			}
		}
		return ""
	}
	return find(n)
}

// walkAttrElement 找匹配 tag+class 的节点，递归收集子节点里的所有文本。
// 容忍 <h3 class="card-title"><a>测试 Chariot</a></h3> 这种直接子节点是元素的情况。
func walkAttrElement(n *html.Node, tag string, class string, fn func(string)) {
	if n.Type == html.ElementNode && n.Data == tag && hasClass(n, class) {
		var sb strings.Builder
		collectText(n, &sb)
		if t := strings.TrimSpace(sb.String()); t != "" {
			fn(t)
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkAttrElement(c, tag, class, fn)
	}
}

// collectText 递归收集节点及其后代的所有文本节点内容（用空格拼接）。
func collectText(n *html.Node, sb *strings.Builder) {
	if n == nil {
		return
	}
	if n.Type == html.TextNode {
		sb.WriteString(n.Data)
		sb.WriteByte(' ')
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, sb)
	}
}

// ———— 兼容旧 API —— 早期 SDK 暴露出 FetchFramehdrDetail，保留以便单条详情补全。

func (a *framehdrAdapter) fetchDetail(ctx context.Context, id string) ([]string, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if !IsConfigured(cfg) {
		return nil, errors.New("帧影站地址未配置")
	}
	target := cfg.BaseURL + "/detail.php?id=" + id
	rawCookie := strings.TrimSpace(strings.ReplaceAll(cfg.Token, framehdrEnrichOffTag, ""))
	body, err := a.http.GetWithCookies(ctx, target, nil, rawCookie)
	if err != nil {
		return nil, fmt.Errorf("帧影详情请求失败：%w", err)
	}
	return extractPanLinks(body), nil
}

// FetchFramehdrDetail 导出：供外部按 ID 钻取详情。
func FetchFramehdrDetail(ctx context.Context, baseURL, cookie, id string) ([]string, error) {
	a := NewFramehdrAdapter().(*framehdrAdapter)
	a.cfg = SiteConfig{Code: SiteFramehdr, BaseURL: baseURL, Token: cookie}
	return a.fetchDetail(ctx, id)
}
