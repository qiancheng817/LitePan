import type { ResourceHubItem } from "@/api/resourceHub";

/** 资源站全部已知代号。 */
export const RESOURCE_HUB_SITES = ["guanying", "jying", "framehdr", "dianying"] as const;
export type ResourceHubSite = (typeof RESOURCE_HUB_SITES)[number];

/** 资源站展示名。 */
export const RESOURCE_HUB_SITE_NAMES: Record<string, string> = {
  guanying: "观影",
  jying: "聚影",
  framehdr: "帧影",
  dianying: "癫影",
};

/** 把搜索结果转成纯 URL（PanSouSaveModal 已有基于 url 的能力判断逻辑可复用）。 */
export function resourceHubItemUrl(item: ResourceHubItem): string {
  return item.url || "";
}

/** 给单条结果打平台 / 站点标签。前台展示使用。 */
export function resourceHubItemTags(item: ResourceHubItem): string[] {
  const out: string[] = [];
  if (item.platform && item.platform !== RESOURCE_HUB_SITE_NAMES[item.source]) {
    out.push(item.platform);
  }
  if (item.source && !out.includes(RESOURCE_HUB_SITE_NAMES[item.source])) {
    out.push(RESOURCE_HUB_SITE_NAMES[item.source] || item.source);
  }
  if (item.tags && item.tags.length) {
    for (const tag of item.tags) {
      if (tag && !out.includes(tag)) out.push(tag);
    }
  }
  return out;
}

/** 后端 pan 平台名 → CLOUD_TYPES code 的映射。 */
const PAN_LABEL_TO_CODE: Record<string, string> = {
  "夸克网盘": "quark",
  "夸克": "quark",
  "115网盘": "115",
  "115": "115",
  "光鸭网盘": "guangya",
  "光鸭云盘": "guangya",
  "光鸭": "guangya",
  "磁链": "magnet",
  "磁力": "magnet",
  "电驴": "ed2k",
  "百度网盘": "baidu",
  "百度": "baidu",
  "阿里网盘": "aliyun",
  "阿里云盘": "aliyun",
  "迅雷网盘": "xunlei",
  "迅雷": "xunlei",
  "天翼网盘": "tianyi",
  "天翼": "tianyi",
  "UC网盘": "uc",
  "UC": "uc",
  "PikPak": "pikpak",
  "123网盘": "123",
  "123": "123",
  "QQ群文件": "qq",
};

/** 从后端的 platform 字段映射到 CLOUD_TYPES code，未识别时返回 null。 */
export function panCodeFromLabel(label: string | null | undefined): string | null {
  if (!label) return null;
  return PAN_LABEL_TO_CODE[label] ?? null;
}

/** 已知网盘 URL 形态 + 展示名。前台打标 + 转存能力匹配复用。
 *  注意：code / label / pattern 均声明为 string，避免 vue-tsc 在模板 v-for 里
 *  把元素类型推成字面联合（否则 `:class`/`setFilter(c.code)` 等位置会触发
 *  "string is not assignable to ..."）。 */
export interface CloudType {
  code: string;
  label: string;
  pattern: RegExp;
}
export const CLOUD_TYPES: CloudType[] = [
  { code: "quark", label: "夸克", pattern: /(?:^|\/\/)(?:[^/]+\.)?quark\.cn\/s\//i },
  { code: "115", label: "115", pattern: /(?:^|\/\/)(?:[^/]+\.)?(?:115\.com|115cdn\.com)\/s\//i },
  { code: "guangya", label: "光鸭", pattern: /(?:^|\/\/)(?:[^/]+\.)?guangyapan\.com\/s\//i },
  { code: "magnet", label: "磁力", pattern: /^magnet:\?xt=/i },
  { code: "ed2k", label: "电驴", pattern: /^ed2k:\/\//i },
  { code: "baidu", label: "百度", pattern: /(?:^|\/\/)pan\.baidu\.com\/s\//i },
  { code: "aliyun", label: "阿里云盘", pattern: /(?:^|\/\/)(?:[^/]+\.)?(?:alipan\.com|aliyundrive\.com)\/s\//i },
  { code: "xunlei", label: "迅雷", pattern: /(?:^|\/\/)(?:[^/]+\.)?pan\.xunlei\.com\/s\//i },
  { code: "tianyi", label: "天翼", pattern: /(?:^|\/\/)cloud\.189\.cn\//i },
  { code: "uc", label: "UC", pattern: /(?:^|\/\/)(?:[^/]+\.)?(?:drive\.uc\.cn|pan\.uc\.cn)\//i },
  { code: "pikpak", label: "PikPak", pattern: /(?:^|\/\/)(?:[^/]+\.)?(?:mypikpak\.com|pikpak\.com)\/s\//i },
  { code: "123", label: "123", pattern: /(?:^|\/\/)(?:[^/]+\.)?123pan\.(?:com|cn)\/s\//i },
  { code: "qq", label: "QQ群", pattern: /(?:^|\/\/)(?:[^/]+\.)?(?:qq\.com|qd\.qq\.com)\/s\//i },
];

/** 从 URL 推断网盘类型，返回 {code,label} 或 null。 */
export function detectCloudType(url: string): { code: string; label: string } | null {
  if (!url) return null;
  for (const t of CLOUD_TYPES) {
    if (t.pattern.test(url)) return { code: t.code, label: t.label };
  }
  return null;
}

/** 判断给定的 URL 是否属于可一键转存到网盘的格式（夸克/115/光鸭/磁力/电驴 + 拓展）。 */
export function isCloudShareUrl(url: string): boolean {
  return detectCloudType(url) !== null;
}

/** 从后端返回的 Tags 中抽出"网盘:xxx"标记（adapter enrich 后写入）。 */
export function panTagFromItem(item: ResourceHubItem): string | null {
  if (!item.tags) return null;
  const tag = item.tags.find((t) => t.startsWith("网盘:"));
  if (!tag) return null;
  const label = tag.slice(3);
  return label || null;
}