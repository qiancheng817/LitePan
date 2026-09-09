package resourcehub

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// httpClient 是适配器共用的 HTTP 客户端：固定超时、自带 User-Agent、内置 Cookie jar。
//
// 设计要点：
//   - 每个 Adapter 实例一个 client，便于隔离会话（特别是聚影站的 csrf/sessionid）。
//   - ResetCookies 用于配置变更时清空过期 Cookie，避免"换账号后旧 token 仍生效"。
type httpClient struct {
	cli     *http.Client
	ua      string
	timeout time.Duration
}

func newHTTPClient(timeout time.Duration) *httpClient {
	if timeout <= 0 {
		timeout = SearchTimeout
	}
	return &httpClient{
		cli: &http.Client{
			Timeout: timeout,
			Jar:     newCookieJar(),
		},
		ua:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36",
		timeout: timeout,
	}
}

// SetBaseTransport 便于测试时替换 Transport（沙箱中默认 Transport 会被禁用）。
func (c *httpClient) SetBaseTransport(t http.RoundTripper) {
	if t == nil {
		c.cli.Transport = nil
		return
	}
	c.cli.Transport = t
}

// SetTimeout 调整后续请求的总超时（已经发出的请求不受影响）。
func (c *httpClient) SetTimeout(d time.Duration) {
	if d > 0 {
		c.cli.Timeout = d
		c.timeout = d
	}
}

// ResetCookies 清空会话 Cookie。下次请求会重新走登录 / 匿名兜底。
func (c *httpClient) ResetCookies() {
	if jar, ok := c.cli.Jar.(*cookieJar); ok {
		jar.Reset()
	}
}

// GetWithCookies 发起 GET，附加可选的原始 Cookie header（用于帧影绕验证码场景）。
//
// 返回响应 body（自动关闭）与状态码。
func (c *httpClient) GetWithCookies(ctx context.Context, target string, headers map[string]string, rawCookie string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	if rawCookie != "" {
		req.Header.Set("Cookie", rawCookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.do(req)
}

// PostForm 发起 application/x-www-form-urlencoded POST。
func (c *httpClient) PostForm(ctx context.Context, target string, form map[string]string, headers map[string]string, rawCookie string) (string, error) {
	body := encodeForm(form)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json,text/plain,*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	if rawCookie != "" {
		req.Header.Set("Cookie", rawCookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.do(req)
}

// PostJSON 发起 application/json POST。
func (c *httpClient) PostJSON(ctx context.Context, target string, jsonBody []byte, headers map[string]string, rawCookie string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(jsonBody))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json,text/plain,*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	if rawCookie != "" {
		req.Header.Set("Cookie", rawCookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.cli.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", resp.StatusCode, ErrRateLimited
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(buf), resp.StatusCode, nil
}

func (c *httpClient) do(req *http.Request) (string, error) {
	resp, err := c.cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", ErrRateLimited
	}
	if resp.StatusCode >= 400 {
		// 部分站点的 401/403 是登录提示，返回 body 由调用方决定怎么处理。
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return string(buf), &httpStatusError{Status: resp.StatusCode, Body: string(buf)}
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

type httpStatusError struct {
	Status int
	Body  string
}

func (e *httpStatusError) Error() string {
	return "HTTP " + http.StatusText(e.Status)
}

// IsStatusError 便于调用方判断具体 HTTP 状态码。
func IsStatusError(err error) (int, bool) {
	var se *httpStatusError
	if errorsAs(err, &se) {
		return se.Status, true
	}
	return 0, false
}

// errorsAs 与标准库 errors.As 等价，避免循环 import。
func errorsAs(err error, target interface{}) bool {
	type asError interface{ As(interface{}) bool }
	if a, ok := err.(asError); ok {
		return a.As(target)
	}
	return false
}

// encodeForm 把 map 编码成 application/x-www-form-urlencoded。
func encodeForm(form map[string]string) string {
	if len(form) == 0 {
		return ""
	}
	var sb strings.Builder
	first := true
	for k, v := range form {
		if !first {
			sb.WriteByte('&')
		}
		first = false
		sb.WriteString(urlEncode(k))
		sb.WriteByte('=')
		sb.WriteString(urlEncode(v))
	}
	return sb.String()
}

// ———— HTML 工具 ————

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// walkText 递归遍历文本节点；fn 收到非空 trimmed 文本。
func walkText(n *html.Node, fn func(string)) {
	if n.Type == html.TextNode {
		fn(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkText(c, fn)
	}
}

// walkAttr 遍历属性；fn 收到属性值。
func walkAttr(n *html.Node, key string, fn func(string)) {
	if key == "" {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			for _, a := range c.Attr {
				fn(a.Val)
			}
			walkAttr(c, "", fn)
		}
		return
	}
	for _, a := range n.Attr {
		if a.Key == key {
			fn(a.Val)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkAttr(c, key, fn)
	}
}

// findFirstTextAfter 寻找紧邻 class=cls 节点的第一个文本节点文本。
// 用于帧影卡片里 <span class="category-badge-overlay">电影</span>。
func findFirstTextAfter(n *html.Node, cls string) string {
	var found bool
	var out string
	var fn func(*html.Node)
	fn = func(node *html.Node) {
		if node.Type == html.ElementNode && hasClass(node, cls) {
			found = true
			// 取该节点自己的文本，不递归 children（避免 title 混入）
			if node.FirstChild != nil && node.FirstChild.Type == html.TextNode {
				out = strings.TrimSpace(node.FirstChild.Data)
			}
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			fn(c)
			if out != "" {
				return
			}
		}
	}
	fn(n)
	_ = found
	return out
}

// urlEncode 与 net/url.QueryEscape 等价，但避免在无网环境下引入额外依赖链路。
func urlEncode(s string) string {
	const upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var sb strings.Builder
	for _, r := range s {
		if r > 0x7f {
			// UTF-8 字节按 % 编码（最小可行方案）
			for _, b := range []byte(string(r)) {
				sb.WriteByte('%')
				const hex = "0123456789ABCDEF"
				sb.WriteByte(hex[b>>4])
				sb.WriteByte(hex[b&0xF])
			}
			continue
		}
		if strings.IndexRune(upper, r) >= 0 {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('%')
			const hexChars = "0123456789ABCDEF"
			sb.WriteByte(hexChars[byte(r)>>4])
			sb.WriteByte(hexChars[byte(r)&0xF])
		}
	}
	return sb.String()
}