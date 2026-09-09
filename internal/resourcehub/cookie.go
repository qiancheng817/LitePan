package resourcehub

import (
	"net/http"
	"net/url"
	"sync"
)

// cookieJar 是 http.Client.Jar 接口的最小实现：仅按 host 维度隔离会话。
//
// 站点登录后服务端下发的 csrftoken/sessionid 等仅在当前 host 下有效，
// 跨 host 隔离避免多个资源站共享同一 Cookie 出现相互污染。
type cookieJar struct {
	mu      sync.Mutex
	buckets map[string][]*http.Cookie
}

func newCookieJar() *cookieJar {
	return &cookieJar{buckets: make(map[string][]*http.Cookie)}
}

func (j *cookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	host := cookieHost(u)
	j.mu.Lock()
	defer j.mu.Unlock()
	existing := j.buckets[host]
	for _, c := range cookies {
		replaced := false
		for i, e := range existing {
			if e.Name == c.Name {
				existing[i] = c
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, c)
		}
	}
	j.buckets[host] = existing
}

func (j *cookieJar) Cookies(u *url.URL) []*http.Cookie {
	host := cookieHost(u)
	j.mu.Lock()
	defer j.mu.Unlock()
	src := j.buckets[host]
	out := make([]*http.Cookie, 0, len(src))
	for _, c := range src {
		if c.MaxAge < 0 {
			continue
		}
		// 同时清理已过期
		if !c.Expires.IsZero() && c.Expires.Before(nowFunc()) {
			continue
		}
		out = append(out, c)
	}
	if len(out) != len(src) {
		j.buckets[host] = out
	}
	return out
}

// Reset 清空全部 Cookie。
func (j *cookieJar) Reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buckets = make(map[string][]*http.Cookie)
}

func cookieHost(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Host
}