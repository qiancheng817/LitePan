<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { http, getApiErrorMessage } from "@/api/client";
import { accountsApi } from "@/api/accounts";
import type { Account } from "@/api/types";
import { resourceHubApi, type ResourceHubItem } from "@/api/resourceHub";
import { offlineDownloadApi } from "@/api/offlineDownload";
import type { OfflineDownloadCapabilities } from "@/types/offline-download";
import AppButton from "@/components/base/AppButton.vue";
import PanSouSaveModal from "@/components/file/PanSouSaveModal.vue";
import { copyTextToClipboard, toast } from "@/composables/useToast";
import {
  CLOUD_TYPES,
  RESOURCE_HUB_SITE_NAMES,
  detectCloudType,
  panCodeFromLabel,
  panTagFromItem,
  resourceHubItemTags,
} from "@/utils/resourceHub";
import { loadPansouAccountCapabilities, pansouSaveCandidates, type PanSouSaveCandidate } from "@/utils/pansouSave";

const props = withDefaults(
  defineProps<{
    /** 账号列表：用于一键转存探测（仅管理员）。 */
    accounts?: { id: number }[];
    isAdmin?: boolean;
  }>(),
  { accounts: () => [], isAdmin: false },
);

const enabled = ref(false);
const q = ref("");
const loading = ref(false);
const searched = ref(false);
const results = ref<ResourceHubItem[]>([]);
const totalGroups = ref(0);
const failures = ref<string[]>([]);
const errorMsg = ref("");
/** 当前激活的网盘过滤器。"" 表示不过滤。 */
const activeFilter = ref("");
/** 转存时是否自动重命名为资源站标题（仅夸克分享且单顶层 entry 生效）。 */
const autoRenameOnSave = ref(true);

onMounted(async () => {
  try {
    const cfg: any = await http.get("/public/system-config");
    enabled.value = Boolean((cfg as any)?.resourcehub_enabled);
    // pansou_auto_rename 缺省视为 true（向后兼容旧版配置）
    const ar = (cfg as any)?.pansou_auto_rename;
    autoRenameOnSave.value = ar === undefined ? true : Boolean(ar);
  } catch {
    /* ignore */
  }
  if (props.isAdmin) {
    void ensureSaveCapabilities();
  }
});

const collapsed = ref(false);
function toggleCollapsed() {
  collapsed.value = !collapsed.value;
}
const collapseHint = computed(() => {
  if (!collapsed.value) return "收起";
  return results.value.length ? `展开（${results.value.length} 条结果）` : "展开";
});

async function search() {
  const keyword = q.value.trim();
  if (!keyword || loading.value) return;
  loading.value = true;
  searched.value = true;
  errorMsg.value = "";
  results.value = [];
  failures.value = [];
  totalGroups.value = 0;
  try {
    const resp = await resourceHubApi.publicSearch(keyword, "all", 1);
    results.value = resp.items || [];
    totalGroups.value = Object.keys(resp.groups || {}).length;
    failures.value = resp.failures || [];
    if (!results.value.length && failures.value.length) {
      errorMsg.value = "全部站点查询失败：" + failures.value.join("；");
    } else if (failures.value.length) {
      // 部分失败：前端 toast 提示，不阻挡结果展示
      toast.error("部分站点失败：" + failures.value.join("；"));
    }
  } catch (e) {
    errorMsg.value = getApiErrorMessage(e, "资源站搜索失败，请稍后重试");
    toast.error(errorMsg.value);
  } finally {
    loading.value = false;
  }
}

async function copyUrl(item: ResourceHubItem) {
  if (!item.url) return;
  const ok = await copyTextToClipboard(item.url);
  if (ok) toast.success("分享链接已复制，可在离线下载 / 分享转存中提交");
}

async function copyPassword(item: ResourceHubItem) {
  if (!item.password) return;
  await copyTextToClipboard(item.password);
}

function isAdminUser(): boolean {
  return props.isAdmin && Array.isArray(props.accounts);
}
void isAdminUser;

const itemTag = resourceHubItemTags;

// —— 一键转存（仅管理员）：复用 PanSouSaveModal ——
const accountList = ref<Account[]>([]);
const capsByAccount = ref<Record<number, OfflineDownloadCapabilities>>({});
const capsLoading = ref(false);
const capsReady = ref(false);
const saveOpen = ref(false);
const saveItem = ref<ResourceHubItem | null>(null);

async function ensureSaveCapabilities() {
  if (capsReady.value) return;
  capsLoading.value = true;
  try {
    const list = (await accountsApi.list()) as Account[];
    accountList.value = list;
    capsByAccount.value = await loadPansouAccountCapabilities(list);
    capsReady.value = true;
  } catch (e) {
    toast.error(getApiErrorMessage(e, "探测账号转存能力失败"));
  } finally {
    capsLoading.value = false;
  }
}

function saveCandidates(item: ResourceHubItem): PanSouSaveCandidate[] {
  if (!isCloudUrl(item)) return [];
  return pansouSaveCandidates(
    { url: item.url, password: item.password } as any,
    accountList.value,
    capsByAccount.value,
  );
}

function isCloudUrl(item: ResourceHubItem): boolean {
  return !!detectCloudType(item.url || "");
}

function saveDisabled(item: ResourceHubItem): boolean {
  if (!isAdminUser()) return true;
  if (capsLoading.value) return true;
  if (!isCloudUrl(item)) return true;
  return saveCandidates(item).length === 0;
}

function saveTitle(item: ResourceHubItem): string {
  if (!isAdminUser()) return "转存功能仅管理员可见";
  if (!isCloudUrl(item)) return "当前 URL 暂不可转存，可复制链接到「离线下载 / 分享转存」手动提交";
  if (capsLoading.value) return "正在探测可转存的网盘账号…";
  const count = saveCandidates(item).length;
  if (count === 0) {
    return "没有可接收该资源的网盘账号：需绑定对应平台的账号（夸克 / 115 支持分享转存，磁力、电驴可转存到支持离线下载的账号）";
  }
  return `一键转存到网盘（命中 ${count} 个账号），可选择目标目录`;
}

function openSave(item: ResourceHubItem) {
  if (saveDisabled(item)) return;
  saveItem.value = item;
  saveOpen.value = true;
}

function cloudLabel(item: ResourceHubItem): string {
  // 优先用后端打好的 platform（adapter 已经把夸克/115/QQ群等识别好了）。
  if (item.platform) return item.platform;
  // 退化：网盘:xxx 标签
  const tagged = panTagFromItem(item);
  if (tagged) return tagged;
  // 退化：从 URL 域名识别
  const detected = detectCloudType(item.url || "");
  return detected ? detected.label : "";
}

function cloudTagClass(item: ResourceHubItem): string {
  // 优先按后端 platform 反查 code（这样 adapter 返回"夸克网盘"也能套上颜色）
  if (item.platform) {
    const code = panCodeFromLabel(item.platform);
    if (code) return `rh-result__pan--${code}`;
  }
  const detected = detectCloudType(item.url || "");
  if (!detected) return "rh-result__pan--unknown";
  return `rh-result__pan--${detected.code}`;
}

// 在结果首列展示前 K 种网盘类型（聚合所有 item url 命中的网盘）。
// 同时也用于顶部"可点击过滤"标签。
const cloudStat = computed<{ code: string; label: string; count: number }[]>(() => {
  const base = CLOUD_TYPES.map((t) => ({ code: t.code, label: t.label, count: 0 }));
  const codeToEntry: Record<string, (typeof base)[number]> = {};
  for (const e of base) codeToEntry[e.code] = e;
  for (const item of results.value) {
    const code = itemPlatformCode(item);
    if (!code) continue;
    const entry = codeToEntry[code];
    if (entry) entry.count++;
  }
  // 追加未映射的 code（例如：观影自带的 "movie"/"tv" 非网盘内部分类），让用户也能筛
  const seen = new Set(base.map((e) => e.code));
  for (const item of results.value) {
    const code = itemPlatformCode(item);
    if (!code || seen.has(code)) continue;
    seen.add(code);
    base.push({ code, label: item.platform || code, count: 1 });
  }
  return base.filter((v) => v.count > 0);
});

/** 取单个 item 的平台 code（用于过滤 / 聚合）。 */
function itemPlatformCode(item: ResourceHubItem): string | null {
  if (!item) return null;
  const code = panCodeFromLabel(item.platform || "");
  if (code) return code;
  const detected = detectCloudType(item.url || "");
  return detected?.code ?? null;
}

/** 顶部过滤栏点击：切换 activeFilter。再次点击同一项取消过滤。 */
function setFilter(code: string) {
  activeFilter.value = activeFilter.value === code ? "" : code;
}

/** 计算过滤后的结果列表。 */
const filteredResults = computed<ResourceHubItem[]>(() => {
  if (!activeFilter.value) return results.value;
  const code = activeFilter.value;
  return results.value.filter((it) => itemPlatformCode(it) === code);
});

/** activeFilter 命中的项数（用于"已过滤 X 条"提示）。 */
const filteredCount = computed(() => filteredResults.value.length);

// offlineDownloadApi 实际由 PanSouSaveModal 通过 props.candidates 自取账号能力，
// 但显式 import 能让 IDE/Vue HMR 检测到类型变化。
void offlineDownloadApi;
</script>

<template>
  <section v-if="enabled" class="rh-panel" :class="{ 'is-collapsed': collapsed }">
    <div
      class="rh-panel__head"
      role="button"
      tabindex="0"
      :aria-expanded="!collapsed"
      :title="collapsed ? '展开资源站搜索面板' : '收起搜索结果，给下方网盘目录腾出空间'"
      @click="toggleCollapsed"
      @keydown.enter.prevent="toggleCollapsed"
      @keydown.space.prevent="toggleCollapsed"
    >
      <div class="rh-panel__titles">
        <h2>资源站</h2>
        <p>聚合观影 / 聚影 / 帧影 / 癫影四个影视资源分享站；右侧「转存」可一键保存到你的网盘目录，或复制链接在离线下载中提交。</p>
      </div>
      <span class="rh-panel__head-side">
        <span class="rh-panel__badge">观影·聚影·帧影·癫影</span>
        <span class="rh-panel__collapse" :class="{ on: collapsed }">
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path
              d="M4 10l4-4 4 4"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
          {{ collapseHint }}
        </span>
      </span>
    </div>

    <div v-show="!collapsed" class="rh-panel__body">
      <div class="rh-panel__search">
        <input
          v-model="q"
          placeholder="搜索电影、电视剧、动漫，如：流浪地球"
          :disabled="loading"
          @keyup.enter="search"
        />
        <AppButton :disabled="loading || !q.trim()" @click="search">
          {{ loading ? "搜索中…" : "搜索" }}
        </AppButton>
      </div>

      <p v-if="errorMsg" class="rh-error">{{ errorMsg }}</p>

      <template v-if="results.length">
        <div class="rh-meta">
          <div class="rh-meta__row">
            <span class="rh-meta__count">共 {{ results.length }} 条结果</span>
            <template v-if="totalGroups > 1">（来自 {{ totalGroups }} 个站点）</template>
            <template v-if="activeFilter && filteredCount !== results.length">
              <span class="rh-meta__sep">·</span>
              <span class="rh-meta__filter-hint">
                已筛 {{ filteredCount }} / {{ results.length }}
              </span>
              <button class="rh-meta__reset" @click="activeFilter = ''">清除筛选</button>
            </template>
          </div>
          <div v-if="cloudStat.length" class="rh-meta__filters">
            <button
              type="button"
              class="rh-filter rh-filter--all"
              :class="{ on: !activeFilter }"
              @click="activeFilter = ''"
            >
              全部 {{ results.length }}
            </button>
            <button
              v-for="c in cloudStat"
              :key="c.code"
              type="button"
              class="rh-filter"
              :class="['rh-filter--' + c.code, { on: activeFilter === c.code }]"
              :title="activeFilter === c.code ? '点击取消过滤' : `只看${c.label}资源`"
              @click="setFilter(c.code)"
            >
              <span class="rh-filter__dot"></span>
              <span class="rh-filter__label">{{ c.label }}</span>
              <span class="rh-filter__count">{{ c.count }}</span>
            </button>
          </div>
        </div>
        <div class="rh-results">
          <div v-for="(r, i) in filteredResults" :key="`${r.source}-${r.url}-${i}`" class="rh-result">
            <div class="rh-result__lead">
              <span class="rh-result__plat">{{ r.platform || RESOURCE_HUB_SITE_NAMES[r.source] || r.source }}</span>
              <span v-if="cloudLabel(r)" class="rh-result__pan" :class="cloudTagClass(r)">{{ cloudLabel(r) }}</span>
            </div>
            <div class="rh-result__body">
              <strong :title="r.title">{{ r.title || "（未命名资源）" }}</strong>
              <small class="rh-result__url" :title="r.url">{{ r.url }}</small>
              <small v-if="itemTag(r).length" class="rh-result__tags">
                <span v-for="t in itemTag(r).filter((x) => !x.startsWith('网盘:'))" :key="`${r.url}-${t}`">{{ t }}</span>
              </small>
            </div>
            <div class="rh-result__actions">
              <button
                v-if="r.password"
                type="button"
                class="rh-result__pwd"
                title="点击复制提取码"
                @click="copyPassword(r)"
              >
                提取码 {{ r.password }}
              </button>
              <button
                v-if="isAdminUser() && isCloudUrl(r)"
                type="button"
                class="rh-result__save"
                :class="{ disabled: saveDisabled(r) }"
                :disabled="saveDisabled(r)"
                :title="saveTitle(r)"
                @click="openSave(r)"
              >
                转存
              </button>
              <button type="button" class="rh-result__copy" @click="copyUrl(r)">复制链接</button>
            </div>
          </div>
        </div>
      </template>

      <p v-if="!loading && !errorMsg && searched && !results.length" class="rh-hint">
        没有搜到相关资源，换个关键词或在「后台 → 增强工具 → 资源站」检查站点配置。
      </p>
      <p v-else-if="!loading && !errorMsg && searched && results.length && !filteredResults.length" class="rh-hint">
        当前筛选下没有结果，
        <a href="#" @click.prevent="activeFilter = ''">清除筛选</a>查看全部 {{ results.length }} 条。
      </p>
      <p v-else-if="!loading && !errorMsg && !searched" class="rh-hint">
        资源站仅展示已配置启用的站点；返回结果若为磁力/电驴，可复制到离线下载里提交。
      </p>
    </div>

    <PanSouSaveModal
      :open="saveOpen"
      :item="saveItem ? {
        url: saveItem.url,
        title: saveItem.title,
        password: saveItem.password || '',
        platform: cloudLabel(saveItem) || RESOURCE_HUB_SITE_NAMES[saveItem.source] || saveItem.source,
        code: saveItem.source,
        source: saveItem.source,
      } as any : null"
      :candidates="saveItem ? saveCandidates(saveItem) : []"
      :rename-on-save="autoRenameOnSave"
      @close="saveOpen = false"
    />
  </section>
</template>

<style scoped>
.rh-panel {
  margin-bottom: 18px;
  padding: 24px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface);
}
.rh-panel__head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  cursor: pointer;
  user-select: none;
  -webkit-user-select: none;
  border-radius: 10px;
}
.rh-panel__head:hover h2 {
  color: var(--primary);
}
.rh-panel__head:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 3px;
}
.rh-panel__titles {
  min-width: 0;
}
.rh-panel__head h2 {
  margin: 0 0 4px;
  font-size: 20px;
  transition: color 0.15s ease;
}
.rh-panel__head p {
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
}
.rh-panel__head-side {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
  margin-top: 2px;
}
.rh-panel__badge {
  color: var(--primary);
  font-size: 12px;
  white-space: nowrap;
}
.rh-panel__collapse {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 11px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface);
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1;
  white-space: nowrap;
  transition: border-color 0.15s ease, color 0.15s ease, background 0.15s ease;
}
.rh-panel__collapse svg {
  width: 13px;
  height: 13px;
  transition: transform 0.2s ease;
}
.rh-panel__head:hover .rh-panel__collapse {
  border-color: var(--primary);
  color: var(--primary);
}
.rh-panel__collapse.on svg {
  transform: rotate(180deg);
}
.rh-panel__collapse.on {
  border-color: var(--primary);
  color: var(--primary);
  background: color-mix(in srgb, var(--primary) 10%, transparent);
}
.rh-panel.is-collapsed {
  padding-bottom: 12px;
}
.rh-panel.is-collapsed .rh-panel__head h2 {
  margin-bottom: 0;
}
.rh-panel.is-collapsed .rh-panel__head p {
  display: none;
}
.rh-panel__search {
  display: flex;
  gap: 10px;
  margin-top: 18px;
}
.rh-panel__search input {
  flex: 1;
  min-width: 0;
  padding: 11px 13px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg);
  color: var(--text);
  outline: none;
}
.rh-panel__search input:focus {
  border-color: var(--primary);
}
.rh-error {
  margin: 12px 0 0;
  color: #e5484d;
  font-size: 13px;
}
.rh-meta {
  margin: 14px 0 0;
  color: var(--text-muted);
  font-size: 13px;
}
.rh-meta__row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.rh-meta__count {
  font-weight: 500;
  color: var(--text);
}
.rh-meta__sep {
  color: var(--border);
}
.rh-meta__filter-hint {
  color: var(--primary);
  font-weight: 500;
}
.rh-meta__reset {
  border: 1px solid var(--primary);
  background: transparent;
  color: var(--primary);
  font-size: 11px;
  padding: 3px 9px;
  border-radius: 999px;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
}
.rh-meta__reset:hover {
  background: var(--primary);
  color: var(--text-on-brand);
}
.rh-meta__filters {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
}
.rh-filter {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 10px;
  border-radius: 999px;
  font-size: 12px;
  line-height: 1.4;
  cursor: pointer;
  background: var(--surface-sunken);
  color: var(--text-muted);
  border: 1px solid var(--border);
  transition: all 0.15s ease;
  user-select: none;
}
.rh-filter:hover {
  transform: translateY(-1px);
  filter: brightness(0.96);
}
.rh-filter.on {
  box-shadow: inset 0 0 0 1px currentColor;
}
.rh-filter__dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 999px;
  background: currentColor;
  opacity: 0.85;
}
.rh-filter__label {
  font-weight: 500;
}
.rh-filter__count {
  font-size: 11px;
  padding: 0 6px;
  border-radius: 999px;
  background: color-mix(in srgb, currentColor 18%, transparent);
  color: currentColor;
  font-weight: 600;
  min-width: 18px;
  text-align: center;
}
.rh-filter--all {
  color: var(--primary);
  background: color-mix(in srgb, var(--primary) 8%, transparent);
  border-color: color-mix(in srgb, var(--primary) 28%, transparent);
}
.rh-filter--all.on {
  background: var(--primary);
  color: var(--text-on-brand);
  border-color: var(--primary);
}
.rh-filter--all.on .rh-filter__count {
  background: color-mix(in srgb, var(--text-on-brand) 25%, transparent);
  color: var(--text-on-brand);
}
/* 各网盘平台：颜色和单条结果卡片保持一致。 */
.rh-filter--quark { color: #1976ff; background: color-mix(in srgb, #1976ff 8%, transparent); border-color: color-mix(in srgb, #1976ff 32%, transparent); }
.rh-filter--115 { color: #ff7a00; background: color-mix(in srgb, #ff7a00 8%, transparent); border-color: color-mix(in srgb, #ff7a00 32%, transparent); }
.rh-filter--guangya { color: #00b894; background: color-mix(in srgb, #00b894 8%, transparent); border-color: color-mix(in srgb, #00b894 32%, transparent); }
.rh-filter--magnet { color: #6c5ce7; background: color-mix(in srgb, #6c5ce7 8%, transparent); border-color: color-mix(in srgb, #6c5ce7 32%, transparent); }
.rh-filter--ed2k { color: #6c5ce7; background: color-mix(in srgb, #6c5ce7 8%, transparent); border-color: color-mix(in srgb, #6c5ce7 32%, transparent); }
.rh-filter--baidu { color: #2980b9; background: color-mix(in srgb, #2980b9 8%, transparent); border-color: color-mix(in srgb, #2980b9 32%, transparent); }
.rh-filter--aliyun { color: #ff6a00; background: color-mix(in srgb, #ff6a00 8%, transparent); border-color: color-mix(in srgb, #ff6a00 32%, transparent); }
.rh-filter--xunlei { color: #ff4757; background: color-mix(in srgb, #ff4757 8%, transparent); border-color: color-mix(in srgb, #ff4757 32%, transparent); }
.rh-filter--tianyi { color: #e84393; background: color-mix(in srgb, #e84393 8%, transparent); border-color: color-mix(in srgb, #e84393 32%, transparent); }
.rh-filter--uc { color: #d63031; background: color-mix(in srgb, #d63031 8%, transparent); border-color: color-mix(in srgb, #d63031 32%, transparent); }
.rh-filter--pikpak { color: #0099ff; background: color-mix(in srgb, #0099ff 8%, transparent); border-color: color-mix(in srgb, #0099ff 32%, transparent); }
.rh-filter--123 { color: #f39c12; background: color-mix(in srgb, #f39c12 8%, transparent); border-color: color-mix(in srgb, #f39c12 32%, transparent); }
.rh-filter--qq { color: #2d8cf0; background: color-mix(in srgb, #2d8cf0 8%, transparent); border-color: color-mix(in srgb, #2d8cf0 32%, transparent); }
.rh-results {
  margin-top: 4px;
}
.rh-result {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  gap: 12px;
  align-items: center;
  padding: 12px 0;
  border-top: 1px solid var(--border-soft);
}
.rh-result__lead {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}
.rh-result__plat {
  align-self: start;
  padding: 3px 8px;
  border-radius: 6px;
  color: var(--primary);
  background: color-mix(in srgb, var(--primary) 10%, transparent);
  font-size: 12px;
  white-space: nowrap;
}
.rh-result__pan {
  align-self: start;
  padding: 2px 7px;
  border-radius: 999px;
  font-size: 11px;
  white-space: nowrap;
  border: 1px solid var(--border);
  background: var(--surface-sunken);
  color: var(--text-muted);
}
.rh-result__pan--quark { color: #1976ff; background: color-mix(in srgb, #1976ff 10%, transparent); border-color: color-mix(in srgb, #1976ff 28%, transparent); }
.rh-result__pan--115 { color: #ff7a00; background: color-mix(in srgb, #ff7a00 10%, transparent); border-color: color-mix(in srgb, #ff7a00 28%, transparent); }
.rh-result__pan--guangya { color: #00b894; background: color-mix(in srgb, #00b894 10%, transparent); border-color: color-mix(in srgb, #00b894 28%, transparent); }
.rh-result__pan--magnet { color: #6c5ce7; background: color-mix(in srgb, #6c5ce7 10%, transparent); border-color: color-mix(in srgb, #6c5ce7 28%, transparent); }
.rh-result__pan--ed2k { color: #6c5ce7; background: color-mix(in srgb, #6c5ce7 10%, transparent); border-color: color-mix(in srgb, #6c5ce7 28%, transparent); }
.rh-result__pan--baidu { color: #2980b9; background: color-mix(in srgb, #2980b9 10%, transparent); border-color: color-mix(in srgb, #2980b9 28%, transparent); }
.rh-result__pan--aliyun { color: #ff6a00; background: color-mix(in srgb, #ff6a00 10%, transparent); border-color: color-mix(in srgb, #ff6a00 28%, transparent); }
.rh-result__pan--xunlei { color: #ff4757; background: color-mix(in srgb, #ff4757 10%, transparent); border-color: color-mix(in srgb, #ff4757 28%, transparent); }
.rh-result__pan--tianyi { color: #e84393; background: color-mix(in srgb, #e84393 10%, transparent); border-color: color-mix(in srgb, #e84393 28%, transparent); }
.rh-result__pan--uc { color: #d63031; background: color-mix(in srgb, #d63031 10%, transparent); border-color: color-mix(in srgb, #d63031 28%, transparent); }
.rh-result__pan--pikpak { color: #0099ff; background: color-mix(in srgb, #0099ff 10%, transparent); border-color: color-mix(in srgb, #0099ff 28%, transparent); }
.rh-result__pan--123 { color: #f39c12; background: color-mix(in srgb, #f39c12 10%, transparent); border-color: color-mix(in srgb, #f39c12 28%, transparent); }
.rh-result__pan--qq { color: #2d8cf0; background: color-mix(in srgb, #2d8cf0 10%, transparent); border-color: color-mix(in srgb, #2d8cf0 28%, transparent); }
.rh-meta__pan {
  margin-left: 6px;
  font-size: 12px;
  color: var(--text-muted);
  white-space: nowrap;
}
.rh-result__body {
  min-width: 0;
  display: grid;
  gap: 3px;
}
.rh-result__body strong {
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rh-result__url {
  color: var(--text-muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rh-result__tags {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 6px;
  color: var(--text-muted);
  font-size: 11px;
}
.rh-result__tags span {
  background: var(--border-soft);
  border-radius: 999px;
  padding: 1px 7px;
}
.rh-result__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  justify-content: flex-end;
}
.rh-result__pwd {
  border: 1px dashed var(--border);
  background: var(--bg);
  color: var(--text-muted);
  font-size: 12px;
  border-radius: 7px;
  padding: 5px 9px;
  cursor: pointer;
}
.rh-result__pwd:hover {
  border-color: var(--primary);
  color: var(--primary);
}
.rh-result__copy {
  border: 0;
  background: none;
  color: var(--primary);
  cursor: pointer;
  font-size: 13px;
  padding: 4px 6px;
  white-space: nowrap;
}
.rh-result__copy:hover {
  text-decoration: underline;
}
.rh-result__save {
  border: 0;
  background: var(--brand-gradient-h);
  color: var(--text-on-brand);
  border-radius: 7px;
  padding: 5px 11px;
  font-size: 13px;
  cursor: pointer;
  transition: opacity 0.15s ease;
  white-space: nowrap;
}
.rh-result__save:hover {
  opacity: 0.9;
}
.rh-result__save.disabled {
  background: var(--border) !important;
  color: var(--text-muted) !important;
  cursor: not-allowed;
}
.rh-result__save.disabled:hover {
  opacity: 1;
}
.rh-hint {
  margin: 16px 0 0;
  color: var(--text-muted);
  font-size: 13px;
}
@media (max-width: 700px) {
  .rh-result {
    grid-template-columns: auto minmax(0, 1fr);
  }
  .rh-result__actions {
    grid-column: 1 / -1;
    justify-content: flex-start;
  }
}
</style>