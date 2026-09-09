package resourcehub

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// guanyingAdapter 观影 (https://www.xn--wcv59z.com · 中文域名：教父.com) 适配器。
//
// 鉴权（实测 2026-09）：
//
//  1. 可选：用户在后台粘贴整段 Cookie（含 browser_verified / app_auth / PHPSESSID）。
//     设置后适配器跳过 PoW 与登录，直接复用 Cookie。
//
//  2. 自动 PoW + 登录：
//     - GET / → 服务端返回 Set-Cookie `browser_pow=xxx`（5 分钟过期）。
//     - GET /res/pow → JSON {N, x, t}（2048-bit 大数模幂）。
//     - 客户端算 y = x^(2^t) mod N，POST /res/pow form-encoded `y` → 服务端
//     返回 {"success":true,"challenge_id":"..."} 并把 browser_pow 升级为
//     browser_verified（表示 PoW 已通过）。
//     - 登录：POST /user/login form-encoded
//     {code:"", siteid:1, dosubmit:1, username, password, cookietime:10506240}
//     → 写 PHPSESSID + app_auth cookie。
//
// 搜索：GET /search?q=<kw> → HTML（嵌入的 _obj.search={...} JSON）。
//
//	_obj.search.l 是并行数组：
//	    title[i]  标题（必有）
//	    ename[i] 别名/外文名
//	    year[i]  年份
//	    d[i]     类型（mv=电影 / tv=剧集 / va=综艺 / ani=动漫 等）
//	    i[i]     资源 ID
//	    pf[i]    评分（嵌套对象 {db:...,im:...}）
//
// 详情钻取：GET /res/downurl/{dir}/{id} → JSON {code, downlist, panlist, playlist, wp}：
//
//	panlist.tname  网盘类型名查表（数组，索引 = type 值）
//	panlist.type   每条记录的网盘代号（索引到 tname）
//	panlist.url    每条记录的完整分享 URL
//	panlist.p      每条记录的提取码
//
// 每个 pan 记录会被展开成独立 Item（Platform=tname[type]），便于前台一键转存。
type guanyingAdapter struct {
	mu   sync.Mutex
	cfg  SiteConfig
	http *httpClient

	powCookie string
	powExpiry time.Time
	enrich    bool
}

func NewGuanyingAdapter() Adapter {
	return &guanyingAdapter{
		http:   newHTTPClient(SearchTimeout),
		enrich: true,
	}
}

func (a *guanyingAdapter) Code() string { return SiteGuanying }

func (a *guanyingAdapter) Meta() SiteMeta {
	return SiteMeta{
		Code:        SiteGuanying,
		Name:        "观影",
		BaseURL:     "https://www.xn--wcv59z.com",
		Description: "观影 · PoW + PbootCMS 密码登录；可粘贴整段 Cookie 跳过自动鉴权；搜索结果来自 GET /search?q=... 内嵌的 _obj.search.l JSON，详情用 GET /res/downurl/{dir}/{id} 拉网盘分享。",
		NeedsAuth:   true,
	}
}

func (a *guanyingAdapter) SetConfig(cfg SiteConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Username != cfg.Username ||
		a.cfg.Password != cfg.Password ||
		a.cfg.Token != cfg.Token {
		a.http.ResetCookies()
		a.powCookie = ""
		a.powExpiry = time.Time{}
	}
	a.cfg = cfg
}

// hasCookieOverride 返回管理员是否提供了可跳过 PoW 的整段 Cookie。
func (a *guanyingAdapter) hasCookieOverride() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.cfg.Token) != ""
}

// cookieOverride 取出管理员提供的整段 Cookie。
func (a *guanyingAdapter) cookieOverride() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.cfg.Token)
}

// solvePoW 走一次 PoW；成功把 y 作为 cookie 的等价值缓存 10h。
func (a *guanyingAdapter) solvePoW(ctx context.Context) error {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if !IsConfigured(cfg) {
		return errors.New("观影站地址未配置")
	}

	body, err := a.http.GetWithCookies(ctx, cfg.BaseURL+"/res/pow", nil, "")
	if err != nil {
		return fmt.Errorf("观影 PoW challenge 获取失败：%w", err)
	}
	var ch struct {
		N string `json:"N"`
		X string `json:"x"`
		T int    `json:"t"`
	}
	if err := json.Unmarshal([]byte(body), &ch); err != nil {
		return fmt.Errorf("观影 PoW challenge 解析失败：%w (body=%s)", err, trimForErr(body))
	}
	if ch.N == "" || ch.X == "" || ch.T <= 0 {
		return fmt.Errorf("观影 PoW challenge 字段缺失：%s", trimForErr(body))
	}

	N, ok := new(big.Int).SetString(ch.N, 16)
	if !ok || N.Sign() <= 0 {
		return errors.New("观影 PoW N 解析失败")
	}
	x, ok := new(big.Int).SetString(ch.X, 16)
	if !ok || x.Sign() < 0 {
		return errors.New("观影 PoW x 解析失败")
	}
	e := new(big.Int).Lsh(big.NewInt(1), uint(ch.T))
	y := new(big.Int).Exp(x, e, N)
	if y.Sign() == 0 {
		return errors.New("观影 PoW 求解结果为 0（异常）")
	}
	yHex := hex.EncodeToString(y.Bytes())

	if _, err := a.http.PostForm(ctx, cfg.BaseURL+"/res/pow",
		map[string]string{"y": yHex}, nil, ""); err != nil {
		return fmt.Errorf("观影 PoW 提交失败：%w", err)
	}

	a.mu.Lock()
	a.powCookie = "y=" + yHex
	a.powExpiry = time.Now().Add(10 * time.Hour)
	a.mu.Unlock()
	return nil
}

func trimForErr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// ensurePoW 缓存命中则跳过；否则重算。
func (a *guanyingAdapter) ensurePoW(ctx context.Context) error {
	a.mu.Lock()
	cookie := a.powCookie
	exp := a.powExpiry
	a.mu.Unlock()
	if cookie != "" && time.Now().Before(exp) {
		return nil
	}
	return a.solvePoW(ctx)
}

// ensureLogin PoW → 表单登录。
func (a *guanyingAdapter) ensureLogin(ctx context.Context) error {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if !HasAuth(cfg) {
		return ErrAuthRequired
	}
	if err := a.ensurePoW(ctx); err != nil {
		return err
	}
	// POST /user/login form-encoded（httpClient 的 jar 会接收 PHPSESSID + app_auth cookie）
	form := map[string]string{
		"code":       "",
		"siteid":     "1",
		"dosubmit":   "1",
		"username":   cfg.Username,
		"password":   cfg.Password,
		"cookietime": "10506240",
	}
	if _, err := a.http.PostForm(ctx, cfg.BaseURL+"/user/login", form, nil, ""); err != nil {
		return fmt.Errorf("观影登录失败：%w", err)
	}
	return nil
}

func (a *guanyingAdapter) Search(ctx context.Context, q string, page int) ([]Item, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if !IsConfigured(cfg) {
		return nil, errors.New("观影站地址未配置")
	}

	useCookie := a.hasCookieOverride()
	if !useCookie {
		if !HasAuth(cfg) {
			return nil, ErrAuthRequired
		}
		if err := a.ensureLogin(ctx); err != nil {
			return nil, err
		}
	}

	u, _ := url.Parse(cfg.BaseURL + "/search")
	q2 := u.Query()
	q2.Set("q", q)
	if page > 1 {
		q2.Set("p", fmt.Sprintf("%d", page))
	}
	u.RawQuery = q2.Encode()

	// Cookie override 走 rawCookie 参数，跳过 jar。
	// 注意：rawCookie 与 jar 互斥；useCookie=true 时我们直接覆盖整套请求 Cookie。
	rawCookie := ""
	if useCookie {
		rawCookie = a.cookieOverride()
	}

	body, err := a.fetchSearch(ctx, u.String(), rawCookie)
	if err != nil {
		if code, ok := IsStatusError(err); ok && (code == httpForbidden || code == httpUnauthorized) {
			a.mu.Lock()
			a.powCookie = ""
			a.powExpiry = time.Time{}
			a.mu.Unlock()
			return nil, fmt.Errorf("观影 PoW/会话失效（HTTP %d），可重试", code)
		}
		return nil, fmt.Errorf("观影搜索失败：%w", err)
	}

	// 401/403 也常见为登录态过期；触发一次重登 + 重试。
	// 当未走 Cookie override 且服务返回 419 PoW challenge JSON（HTTP 200），重算一次 PoW 后再请求。
	if !useCookie && a.isPoWExpiredBody(body) {
		a.mu.Lock()
		a.powCookie = ""
		a.powExpiry = time.Time{}
		a.mu.Unlock()
		if err2 := a.ensurePoW(ctx); err2 == nil {
			if retry, err3 := a.fetchSearch(ctx, u.String(), ""); err3 == nil {
				body = retry
			}
		}
	}

	// "未登录，访问受限" 单独识别：仍然重试一次登录。
	if !useCookie && strings.Contains(body, "未登录，访问受限") {
		if err2 := a.ensureLogin(ctx); err2 == nil {
			if retry, err3 := a.fetchSearch(ctx, u.String(), ""); err3 == nil {
				body = retry
			}
		}
	}

	items, err := parseGuanyingSearch(body, cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Source = SiteGuanying
		if items[i].Code == "" {
			items[i].Code = SiteGuanying
		}
		if items[i].Platform == "" {
			items[i].Platform = "观影"
		}
	}

	// 详情钻取：每条 Item 用 /res/downurl/{dir}/{id} 拿真实分享链接，展开成多个 Item。
	if a.enrich && len(items) > 0 {
		if expanded := a.expandItemsWithPanlists(ctx, items, rawCookie); len(expanded) > 0 {
			items = expanded
		}
	}
	return items, nil
}

// fetchSearch 是带 rawCookie 的统一搜索请求。rawCookie 为空时走 jar 自管理。
func (a *guanyingAdapter) fetchSearch(ctx context.Context, target, rawCookie string) (string, error) {
	return a.http.GetWithCookies(ctx, target, map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/json,*/*",
	}, rawCookie)
}

// isPoWExpiredBody 识别 419 PoW challenge JSON 响应。
var powExpiredBodyRe = regexp.MustCompile(`"code"\s*:\s*419`)

func (a *guanyingAdapter) isPoWExpiredBody(body string) bool {
	if body == "" {
		return false
	}
	return powExpiredBodyRe.MatchString(body)
}

// guanyingSearch 观影 _obj.search 的反序列化形态。
type guanyingSearch struct {
	Q string             `json:"q"`
	L guanyingSearchItem `json:"l"`
}

// guanyingSearchItem 嵌套在 l 下的并行数组。
type guanyingSearchItem struct {
	Title []string `json:"title"`
	EName []string `json:"ename"`
	Name  []string `json:"name"`
	Year  []any    `json:"year"`
	D     []string `json:"d"`
	I     []string `json:"i"`
	PF    any      `json:"pf"`
}

// guanyingObjExpr 在 HTML 里抓到 _obj.search = {...} 这一 JSON 字面量。
var guanyingObjExpr = regexp.MustCompile(`_obj\.search\s*=\s*\{([\s\S]*?)\}\s*;`)

// parseGuanyingSearch 从 /search?q=... 的 HTML 中提取 _obj.search JSON，
// 把并行数组 zip 成 Item 列表。
//
// Item.URL  = {BaseURL}/{d[i]}/{i[i]}（如 /mv/jZX6）。
// Item.Tags = [类型(m->电影 / tv->剧集), 年份, 评分]（如有）。
func parseGuanyingSearch(body, baseURL string) ([]Item, error) {
	m := guanyingObjExpr.FindStringSubmatch(body)
	if len(m) < 2 {
		return nil, fmt.Errorf("观影搜索响应未包含 _obj.search：%s", trimForErr(body))
	}
	raw := "{" + m[1] + "}"
	jsonStr := quoteJSKeys(raw)

	var s guanyingSearch
	if err := json.Unmarshal([]byte(jsonStr), &s); err != nil {
		return nil, fmt.Errorf("观影 _obj.search 解析失败：%w (raw=%s)", err, trimForErr(raw))
	}

	titles := s.L.Title
	n := len(titles)
	out := make([]Item, 0, n)
	for i := 0; i < n; i++ {
		title := strings.TrimSpace(titles[i])
		if title == "" {
			continue
		}
		var id, cat string
		if i < len(s.L.I) {
			id = strings.TrimSpace(s.L.I[i])
		}
		if i < len(s.L.D) {
			cat = strings.TrimSpace(s.L.D[i])
		}
		if id == "" {
			continue
		}
		var tags []string
		if cat != "" {
			tags = append(tags, guanyingCatName(cat))
		}
		if i < len(s.L.Year) {
			if y := asInt(s.L.Year[i]); y > 0 {
				tags = append(tags, fmt.Sprintf("%d", y))
			}
		}
		if i < len(s.L.EName) {
			if en := strings.TrimSpace(s.L.EName[i]); en != "" && len(en) <= 120 {
				tags = append(tags, "别名:"+en)
			}
		}
		path := cat
		if path == "" {
			path = "mv"
		}
		out = append(out, Item{
			Title: title,
			URL:   strings.TrimRight(baseURL, "/") + "/" + path + "/" + id,
			Tags:  tags,
		})
	}
	if len(out) == 0 {
		return nil, ErrEmpty
	}
	return out, nil
}

// guanyingCatName 把英文类型代号转成中文标签（仅用于展示）。
func guanyingCatName(code string) string {
	switch code {
	case "mv":
		return "电影"
	case "tv":
		return "剧集"
	case "va":
		return "综艺"
	case "ani":
		return "动漫"
	default:
		return code
	}
}

// expandItemsWithPanlists 对每条 Item 调 /res/downurl/{dir}/{id} 拿真实网盘分享，
// 把返回的 panlist 数组展开成多个 Item（每个 pan 一条）。
//
// 设计要点：
//   - 当 panlist 为空或拉取失败时，保留原 Item（URL 仍是详情页，前台可点进去手动复制）。
//   - 并发上限 4，避免压垮上游。
//   - 用 rawCookie 走 Cookie override；为空时走 jar。
//   - 返回展开后的新 slice（可能比原 slice 长，调用方需用返回值）。
func (a *guanyingAdapter) expandItemsWithPanlists(ctx context.Context, items []Item, rawCookie string) []Item {
	// 用 chan + 一组 goroutine 收集每条 Item 的展开结果，
	// 结果按 index 还原顺序。
	type enriched struct {
		idx  int
		list []Item
	}
	collected := make(chan enriched, len(items))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range items {
		dir, id, ok := splitGuanyingPath(items[i].URL)
		if !ok {
			collected <- enriched{idx: i, list: []Item{items[i]}}
			continue
		}
		idx := i
		title := items[i].Title
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, dir, id, title string) {
			defer wg.Done()
			defer func() { <-sem }()
			body, err := a.fetchDownURL(ctx, dir, id, rawCookie)
			if err != nil {
				collected <- enriched{idx: idx, list: []Item{items[idx]}}
				return
			}
			expanded := parseGuanyingDownURL(body, title, dir)
			if len(expanded) == 0 {
				collected <- enriched{idx: idx, list: []Item{items[idx]}}
				return
			}
			collected <- enriched{idx: idx, list: expanded}
		}(idx, dir, id, title)
	}
	wg.Wait()
	close(collected)

	out2 := make([]Item, 0, len(items)*2)
	for r := range collected {
		out2 = append(out2, r.list...)
	}
	return out2
}

// fetchDownURL GET /res/downurl/{dir}/{id}。
func (a *guanyingAdapter) fetchDownURL(ctx context.Context, dir, id, rawCookie string) (string, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	target := fmt.Sprintf("%s/res/downurl/%s/%s", strings.TrimRight(cfg.BaseURL, "/"), dir, id)
	return a.http.GetWithCookies(ctx, target, map[string]string{
		"Accept": "application/json,text/plain,*/*",
	}, rawCookie)
}

// splitGuanyingPath 把 {BaseURL}/{dir}/{id} 解析成 dir + id。
// 例：https://www.xn--wcv59z.com/mv/jZX6 → ("mv", "jZX6")。
func splitGuanyingPath(raw string) (string, string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// guanyingDownURLResp /res/downurl/{dir}/{id} 的响应形态。
type guanyingDownURLResp struct {
	Code     int              `json:"code"`
	Msg      string           `json:"msg"`
	Panlist  *guanyingPanlist `json:"panlist"`
	Downlist *json.RawMessage `json:"downlist"`
	Playlist *json.RawMessage `json:"playlist"`
	WP       *json.RawMessage `json:"wp"`
}

// guanyingPanlist panlist 字段，是并行数组。
type guanyingPanlist struct {
	ID    []string `json:"id"`
	Name  []string `json:"name"`
	P     []string `json:"p"`    // 提取码（"无提取码" 表示空）
	URL   []string `json:"url"`  // 完整分享 URL（部分链接里已带 ?pwd=...）
	Type  []any    `json:"type"` // 类型代号（索引到 tname）
	User  []string `json:"user"`
	TName []string `json:"tname"` // 网盘显示名查表
}

// parseGuanyingDownURL 把 /res/downurl 响应展开成多个 Item。
//
// 每个 pan 记录成为一条 Item：
//   - URL = panlist.url[i]
//   - Password = panlist.p[i]（"无提取码" → 空）
//   - Platform = tname[type[i]]
//   - Code = SiteGuanying（来源相同）
//   - Source = SiteGuanying
//   - Tags 含原 title + 元数据标签
//
// 当 type[i] 越界或 tname 为空时，platform 退化用正则从 URL 域名识别。
func parseGuanyingDownURL(body, parentTitle, dir string) []Item {
	var resp guanyingDownURLResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil
	}
	if resp.Code != 200 || resp.Panlist == nil {
		return nil
	}
	pl := resp.Panlist
	n := len(pl.URL)
	if n == 0 || len(pl.ID) != n || len(pl.Type) != n {
		return nil
	}
	out := make([]Item, 0, n)
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		shareURL := strings.TrimSpace(pl.URL[i])
		if shareURL == "" {
			continue
		}
		// 去重：相同 URL 只保留第一条
		if seen[shareURL] {
			continue
		}
		seen[shareURL] = true

		// 平台名：优先 tname[type[i]]，否则按 URL 域名识别
		platform := ""
		if len(pl.TName) > 0 {
			if tIdx, ok := asIntAny(pl.Type[i]); ok && tIdx >= 0 && tIdx < len(pl.TName) {
				platform = pl.TName[tIdx]
			}
		}
		if platform == "" {
			platform = detectPanFromURL(shareURL)
		}
		if platform == "" {
			platform = "网盘"
		}

		// 提取码：先从 p 字段取（去掉"无提取码"占位），否则从 URL query 抓 pwd=
		pwd := strings.TrimSpace(pl.P[i])
		if pwd == "无提取码" {
			pwd = ""
		}
		if pwd == "" {
			if u, err := url.Parse(shareURL); err == nil {
				pwd = u.Query().Get("pwd")
			}
		}

		item := Item{
			Code:     SiteGuanying,
			Source:   SiteGuanying,
			Platform: platform,
			Title:    parentTitle,
			URL:      shareURL,
			Password: pwd,
			Tags:     []string{guanyingCatName(dir)},
		}
		out = append(out, item)
	}
	return out
}

// asIntAny 把 JSON number 转 int，附带 ok 表示类型是否转换成功。
func asIntAny(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	case int:
		return x, true
	}
	return 0, false
}

// detectPanFromURL 从 URL 域名识别网盘类型。
func detectPanFromURL(u string) string {
	lu := strings.ToLower(u)
	switch {
	case strings.Contains(lu, "pan.quark.cn"):
		return "夸克网盘"
	case strings.Contains(lu, "115.com"), strings.Contains(lu, "115cdn.com"), strings.Contains(lu, "114.115.com"):
		return "115网盘"
	case strings.Contains(lu, "guangyapan.com"):
		return "光鸭网盘"
	case strings.Contains(lu, "pan.baidu.com"):
		return "百度网盘"
	case strings.Contains(lu, "pan.xunlei.com"), strings.Contains(lu, "xunlei.com"):
		return "迅雷网盘"
	case strings.Contains(lu, "aliyundrive.com"), strings.Contains(lu, "alipan.com"):
		return "阿里网盘"
	case strings.Contains(lu, "cloud.189.cn"):
		return "天翼网盘"
	case strings.Contains(lu, "drive.uc.cn"), strings.Contains(lu, "pan.uc.cn"):
		return "UC网盘"
	case strings.Contains(lu, "pikpak.com"), strings.Contains(lu, "mypikpak.com"):
		return "PikPak"
	case strings.Contains(lu, "123pan.com"), strings.Contains(lu, "123684.com"):
		return "123网盘"
	case strings.Contains(lu, "magnet:"):
		return "磁链"
	case strings.Contains(lu, "ed2k://"):
		return "电驴"
	case strings.Contains(lu, "qd.qq.com"), strings.Contains(lu, "qq.com"):
		return "QQ群文件"
	}
	return ""
}

// quoteJSKeys 把 JS 字面量里未加引号的键名加双引号，变成合法 JSON。
func quoteJSKeys(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 64)
	inStr := false
	escape := false
	i := 0
	for i < len(s) {
		c := s[i]
		if inStr {
			sb.WriteByte(c)
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inStr = false
			}
			i++
			continue
		}
		switch c {
		case '"':
			inStr = true
			sb.WriteByte(c)
			i++
		case '{', ',':
			sb.WriteByte(c)
			i++
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && isIdentStart(s[j]) {
				sb.WriteByte('"')
				for j < len(s) && isIdentPart(s[j]) {
					sb.WriteByte(s[j])
					j++
				}
				sb.WriteByte('"')
				i = j
			} else {
				i = j
			}
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return sb.String()
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == '$'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
