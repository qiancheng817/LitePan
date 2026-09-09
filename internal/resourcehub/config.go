package resourcehub

import (
	"strings"

	"litepan/internal/settings"
)

// LoadSites 从全局设置读取各站点配置（敏感字段走 StringAllowEmpty 以保留显式空值）。
func LoadSites(s *settings.Service) (map[string]SiteConfig, map[string]bool) {
	enabled := SetEnabled(s.String(settings.KeyResourceHubSites))
	cfgs := make(map[string]SiteConfig, len(AllSites))
	cfgs[SiteGuanying] = SiteConfig{
		Code:     SiteGuanying,
		BaseURL:  strings.TrimRight(strings.TrimSpace(s.String(settings.KeyResourceHubGuanyingURL)), "/"),
		Username: s.String(settings.KeyResourceHubGuanyingUser),
		Password: s.StringAllowEmpty(settings.KeyResourceHubGuanyingPwd),
		Token:    s.StringAllowEmpty(settings.KeyResourceHubGuanyingCookie),
	}
	cfgs[SiteJying] = SiteConfig{
		Code:     SiteJying,
		BaseURL:  strings.TrimRight(strings.TrimSpace(s.String(settings.KeyResourceHubJyingURL)), "/"),
		Username: s.String(settings.KeyResourceHubJyingUser),
		Password: s.StringAllowEmpty(settings.KeyResourceHubJyingPwd),
		AppKey:   s.StringAllowEmpty(settings.KeyResourceHubJyingAppKey),
	}
	cfgs[SiteFramehdr] = SiteConfig{
		Code:     SiteFramehdr,
		BaseURL:  strings.TrimRight(strings.TrimSpace(s.String(settings.KeyResourceHubFramehdrURL)), "/"),
		Username: s.String(settings.KeyResourceHubFramehdrUser),
		Password: s.StringAllowEmpty(settings.KeyResourceHubFramehdrPwd),
		Token:    s.StringAllowEmpty(settings.KeyResourceHubFramehdrToken),
	}
	return cfgs, enabled
}

// IsConfigured 返回站点是否具备运行所需最小配置（BaseURL）。
func IsConfigured(cfg SiteConfig) bool {
	return strings.TrimSpace(cfg.BaseURL) != ""
}

// HasAuth 返回站点是否提供了登录凭据（账号密码 或 Cookie Token）。
func HasAuth(cfg SiteConfig) bool {
	if strings.TrimSpace(cfg.Token) != "" {
		return true
	}
	return strings.TrimSpace(cfg.Username) != "" && cfg.Password != ""
}