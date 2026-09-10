import { http } from "./client";

/** 资源站单站点配置（GET 时不含敏感字段，仅 password_configured/token_configured 标记）。 */
export interface ResourceHubSiteConfig {
  url: string;
  username: string;
  password_configured: boolean;
  token_configured: boolean;
  cookie_configured?: boolean;
  app_key_configured?: boolean;
}

/** 资源站总配置。 */
export interface ResourceHubConfig {
  enabled: boolean;
  /** 当前已启用站点代号（与 Options 中的 code 对应）。 */
  sites: string[];
  /** 全量站点元信息（用于后台渲染每个站点的状态/备注）。 */
  options: ResourceHubSiteMeta[];
  framehdr: ResourceHubSiteConfig;
  jying: ResourceHubSiteConfig;
  guanying: ResourceHubSiteConfig;
  dianying: ResourceHubSiteConfig;
}

/** 单站点元信息，与后端 SiteMeta 字段一致。 */
export interface ResourceHubSiteMeta {
  code: string;
  name: string;
  base_url: string;
  description: string;
  needs_auth: boolean;
  available: boolean;
  note?: string;
}

/** 单条搜索结果（统一格式，对齐 PanSouItem）。 */
export interface ResourceHubItem {
  code: string;
  platform: string;
  title: string;
  url: string;
  password?: string;
  tags?: string[];
  /** 来源站点（guanying/jying/framehdr），便于前台分组或筛选。 */
  source: string;
}

/** 搜索响应。 */
export interface ResourceHubSearchResponse {
  items: ResourceHubItem[];
  groups: Record<string, ResourceHubItem[]>;
  failures: string[];
  q: string;
  page: number;
}

/** 后台连通性测试请求。 */
export interface ResourceHubTestPayload {
  site: string;
  q: string;
  url?: string;
  username?: string;
  password?: string;
  token?: string;
  cookie?: string;
  app_key?: string;
}

/** 后台连通性测试响应。 */
export interface ResourceHubTestResponse {
  count: number;
  preview: ResourceHubItem[];
}

export interface ResourceHubSavePayload {
  enabled: boolean;
  sites: string[];
  guanying_url: string;
  guanying_username: string;
  guanying_password: string;
  guanying_cookie?: string;
  jying_url: string;
  jying_username: string;
  jying_password: string;
  jying_app_key?: string;
  framehdr_url: string;
  framehdr_username: string;
  framehdr_password: string;
  framehdr_token: string;
  dianying_url: string;
  dianying_username: string;
  dianying_password: string;
  dianying_token: string;
  dianying_cookie: string;
}

export const resourceHubApi = {
  getConfig: () => http.get<ResourceHubConfig>("/admin/tools/resourcehub/config"),
  saveConfig: (payload: ResourceHubSavePayload) =>
    http.put<ResourceHubConfig>("/admin/tools/resourcehub/config", payload),
  /** 后台连通性测试：传临时配置可走未保存的 URL/凭据。 */
  testSite: (payload: ResourceHubTestPayload) =>
    http.post<ResourceHubTestResponse>("/admin/tools/resourcehub/test", payload),
  /** 前台首页用的搜索（公共接口，启用资源站总开关才允许）。 */
  publicSearch: (q: string, site = "all", page = 1) =>
    http.get<ResourceHubSearchResponse>("/public/tools/resourcehub/search", { q, site, page }),
};