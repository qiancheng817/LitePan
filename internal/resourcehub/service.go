package resourcehub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"litepan/internal/settings"
)

// Service 资源站聚合服务，封装各站点 Adapter 的查找、调用与配置读取。
//
// 线程安全：内部对适配器与配置使用互斥锁保护；适配器自身也需保证并发安全。
type Service struct {
	log       *slog.Logger
	settings  *settings.Service
	mu        sync.RWMutex
	adapters  map[string]Adapter
	configs   map[string]SiteConfig
	enabled   map[string]bool
}

// New 创建资源站服务并注册全部适配器。setts 可为 nil（用于测试）。
func New(log *slog.Logger, setts *settings.Service) *Service {
	s := &Service{
		log:      log,
		settings: setts,
		adapters: make(map[string]Adapter),
		configs:  make(map[string]SiteConfig, len(AllSites)),
		enabled:  SetEnabled(""),
	}
	s.register(NewGuanyingAdapter())
	s.register(NewJyingAdapter())
	s.register(NewFramehdrAdapter())
	s.register(NewDianyingAdapter())
	s.Refresh()
	return s
}

// SetLogger 装配期注入 config 模块 logger。
func (s *Service) SetLogger(log *slog.Logger) {
	s.log = log
}

// register 注册适配器。重复注册会覆盖。
func (s *Service) register(a Adapter) {
	s.adapters[a.Code()] = a
}

// Refresh 从 settings 重新加载站点配置与启用状态。
func (s *Service) Refresh() {
	if s.settings == nil {
		return
	}
	cfgs, enabled := LoadSites(s.settings)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs = cfgs
	s.enabled = enabled
}

// Enabled 返回资源站总开关。
func (s *Service) Enabled() bool {
	if s.settings == nil {
		return false
	}
	return s.settings.Bool(settings.KeyResourceHubEnabled)
}

// Sites 返回前端展示用的全部站点元信息（含启用状态、配置完整度、备注）。
func (s *Service) Sites() []SiteMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SiteMeta, 0, len(AllSites))
	for _, code := range AllSites {
		adapter, ok := s.adapters[code]
		if !ok {
			continue
		}
		meta := adapter.Meta()
		meta.BaseURL = s.configs[code].BaseURL
		meta.Available = s.Enabled() && s.enabled[code] && IsConfigured(s.configs[code])
		switch code {
		case SiteGuanying:
			if !HasAuth(s.configs[code]) {
				meta.Note = "观影站前端有 PoW 验证，自动登录受限；当前仅暴露接口，未对接入"
			}
		case SiteJying:
			if !HasAuth(s.configs[code]) {
				meta.Note = "聚影站搜索需登录：未配置账号时无法返回结果"
			}
		case SiteFramehdr:
			if !HasAuth(s.configs[code]) {
				meta.Note = "帧影站支持匿名搜索；登录后可解锁更多内容"
			}
		case SiteDianying:
			if !HasAuth(s.configs[code]) {
				meta.Note = "癫影 OpenAPI 需 VIP 创建 API Key；未配置 Token 时无法搜索"
			}
		}
		out = append(out, meta)
	}
	return out
}

// SitesPublic 返回前台可见的站点列表（仅展示已启用且配置完整）。
func (s *Service) SitesPublic() []SiteMeta {
	all := s.Sites()
	out := make([]SiteMeta, 0, len(all))
	for _, m := range all {
		if m.Available {
			out = append(out, m)
		}
	}
	return out
}

// GetAdapter 返回指定站点的适配器（用于测试 / 高级调用）。
func (s *Service) GetAdapter(code string) (Adapter, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.adapters[code]
	return a, ok
}

// Search 调用单个站点的搜索。
//
// page < 1 时视为 1。nil cfg 表示使用当前已加载的配置。
func (s *Service) Search(ctx context.Context, code, q string, page int) ([]Item, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	s.mu.RLock()
	enabled := s.enabled[code]
	cfg := s.configs[code]
	adapter, ok := s.adapters[code]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("未知站点：%s", code)
	}
	if !enabled {
		return nil, ErrDisabled
	}
	if !IsConfigured(cfg) {
		return nil, fmt.Errorf("站点 %s 未配置地址", code)
	}
	adapter.SetConfig(cfg)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("搜索关键词不能为空")
	}
	if page < 1 {
		page = 1
	}
	ctx, cancel := context.WithTimeout(ctx, SearchTimeout)
	defer cancel()
	if s.log != nil {
		s.log.Info("resourcehub search", "site", code, "q", q, "page", page)
	}
	return adapter.Search(ctx, q, page)
}

// SearchAll 并行调用已启用的站点，结果打平。前台首页只关心可视化效果。
//
// 单个站点失败不影响其他站点返回；返回的 error 表示"全部失败"时的最后错误。
func (s *Service) SearchAll(ctx context.Context, q string, page int) (map[string][]Item, []string) {
	s.mu.RLock()
	enabled := s.enabled
	configs := s.configs
	s.mu.RUnlock()
	if !s.Enabled() {
		return map[string][]Item{}, []string{ErrDisabled.Error()}
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return map[string][]Item{}, []string{"搜索关键词不能为空"}
	}

	var wg sync.WaitGroup
	results := make(map[string][]Item, len(AllSites))
	var failMu sync.Mutex
	failures := make([]string, 0)
	for _, code := range AllSites {
		if !enabled[code] {
			continue
		}
		if !IsConfigured(configs[code]) {
			continue
		}
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			items, err := s.Search(ctx, code, q, page)
			if err != nil {
				failMu.Lock()
				failures = append(failures, fmt.Sprintf("%s: %s", code, err.Error()))
				failMu.Unlock()
				return
			}
			results[code] = items
		}(code)
	}
	wg.Wait()
	if len(results) == 0 && len(failures) == 0 {
		failures = append(failures, ErrEmpty.Error())
	}
	return results, failures
}