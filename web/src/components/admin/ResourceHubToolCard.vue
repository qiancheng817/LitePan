<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { http, getApiErrorMessage } from "@/api/client";
import {
  resourceHubApi,
  type ResourceHubConfig,
  type ResourceHubItem,
  type ResourceHubSavePayload,
} from "@/api/resourceHub";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import CloudToolCard from "@/components/admin/CloudToolCard.vue";
import { toast } from "@/composables/useToast";
import { RESOURCE_HUB_SITE_NAMES } from "@/utils/resourceHub";

const props = withDefaults(defineProps<{ searchQuery?: string }>(), { searchQuery: "" });

interface SiteForm {
  enabled: boolean;
  url: string;
  username: string;
  password: string;
  token: string;
  cookie: string;
  appKey: string;
}

const cfg = ref<ResourceHubConfig | null>(null);
const saving = ref(false);
const configOpen = ref(false);
const savingConfig = ref(false);

const draft = reactive<{
  enabled: boolean;
  sites: Record<string, boolean>;
  guanying: SiteForm;
  jying: SiteForm;
  framehdr: SiteForm;
  dianying: SiteForm;
}>({
  enabled: false,
  sites: { guanying: false, jying: false, framehdr: false, dianying: false },
  guanying: emptyForm(),
  jying: emptyForm(),
  framehdr: emptyForm(),
  dianying: emptyForm(),
});

function emptyForm(): SiteForm {
  return { enabled: false, url: "", username: "", password: "", token: "", cookie: "", appKey: "" };
}

function matches(title: string) {
  const q = props.searchQuery.trim().toLowerCase();
  return !q || title.toLowerCase().includes(q);
}

const enabledCount = computed(() => Object.values(draft.sites).filter(Boolean).length);

async function load() {
  try {
    const data = await resourceHubApi.getConfig();
    cfg.value = data;
    applyDraft(data);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "加载资源站设置失败"));
  }
}

function applyDraft(data: ResourceHubConfig) {
  draft.enabled = data.enabled;
  draft.sites = { guanying: false, jying: false, framehdr: false, dianying: false };
  for (const code of data.sites || []) {
    if (draft.sites[code] !== undefined) draft.sites[code] = true;
  }
  draft.guanying = {
    enabled: draft.sites.guanying,
    url: data.guanying?.url ?? "",
    username: data.guanying?.username ?? "",
    password: "",
    token: "",
    cookie: "",
    appKey: "",
  };
  draft.jying = {
    enabled: draft.sites.jying,
    url: data.jying?.url ?? "",
    username: data.jying?.username ?? "",
    password: "",
    token: "",
    cookie: "",
    appKey: "",
  };
  draft.framehdr = {
    enabled: draft.sites.framehdr,
    url: data.framehdr?.url ?? "",
    username: data.framehdr?.username ?? "",
    password: "",
    token: "",
    cookie: "",
    appKey: "",
  };
  draft.dianying = {
    enabled: draft.sites.dianying,
    url: data.dianying?.url ?? "",
    username: data.dianying?.username ?? "",
    password: "",
    token: data.dianying?.token_configured ? "" : "",
    cookie: "",
    appKey: "",
  };
}

onMounted(load);

async function toggleEnabled() {
  if (saving.value) return;
  saving.value = true;
  const next = !draft.enabled;
  try {
    await saveAll({ enabledOverride: next });
    draft.enabled = next;
    toast.success(next ? "已启用：前台首页将显示资源站搜索入口" : "已停用：前台首页的资源站入口已隐藏");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "修改开关失败"));
  } finally {
    saving.value = false;
  }
}

function openConfig() {
  if (cfg.value) applyDraft(cfg.value);
  configOpen.value = true;
}

function closeConfig() {
  configOpen.value = false;
}

async function saveAll(opts: { enabledOverride?: boolean; successMessage?: string } = {}) {
  const enabledSites: string[] = [];
  for (const code of ["guanying", "jying", "framehdr", "dianying"] as const) {
    if (draft.sites[code]) enabledSites.push(code);
  }
  const payload: ResourceHubSavePayload = {
    enabled: opts.enabledOverride ?? draft.enabled,
    sites: enabledSites,
    guanying_url: draft.guanying.url,
    guanying_username: draft.guanying.username,
    guanying_password: draft.guanying.password,
    guanying_cookie: draft.guanying.cookie,
    jying_url: draft.jying.url,
    jying_username: draft.jying.username,
    jying_password: draft.jying.password,
    jying_app_key: draft.jying.appKey,
    framehdr_url: draft.framehdr.url,
    framehdr_username: draft.framehdr.username,
    framehdr_password: draft.framehdr.password,
    framehdr_token: draft.framehdr.token,
    dianying_url: draft.dianying.url,
    dianying_username: draft.dianying.username,
    dianying_password: draft.dianying.password,
    dianying_token: draft.dianying.token,
    dianying_cookie: draft.dianying.cookie,
  };
  const next = await resourceHubApi.saveConfig(payload);
  cfg.value = next;
  applyDraft(next);
  toast.success(opts.successMessage || "资源站设置已保存");
}

async function onSave() {
  savingConfig.value = true;
  try {
    await saveAll();
    configOpen.value = false;
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存资源站设置失败"));
  } finally {
    savingConfig.value = false;
  }
}

// —— 连通性测试 ——
const testing = ref(false);
const testKw = ref("");
const testError = ref("");
const testItems = ref<ResourceHubItem[]>([]);

async function testSite(code: "guanying" | "jying" | "framehdr" | "dianying") {
  if (testing.value) return;
  const form = draft[code];
  const q = testKw.value.trim();
  if (!q) {
    testError.value = "请先在上方输入测试关键词";
    return;
  }
  testing.value = true;
  testError.value = "";
  testItems.value = [];
  try {
    const res = await resourceHubApi.testSite({
      site: code,
      q,
      url: form.url,
      username: form.username,
      password: form.password,
      token: form.token,
      cookie: form.cookie,
      app_key: form.appKey,
    });
    testItems.value = res.preview;
    if (!res.count) {
      testError.value = "连通正常，但没有搜到结果，可换个关键词或减少站点再试";
    } else {
      toast.success(`${RESOURCE_HUB_SITE_NAMES[code] || code} 返回 ${res.count} 条结果`);
    }
  } catch (e) {
    testError.value = getApiErrorMessage(e, "测试失败");
  } finally {
    testing.value = false;
  }
}

const siteOptions: { code: "guanying" | "jying" | "framehdr" | "dianying"; label: string }[] = [
  { code: "guanying", label: RESOURCE_HUB_SITE_NAMES.guanying },
  { code: "jying", label: RESOURCE_HUB_SITE_NAMES.jying },
  { code: "framehdr", label: RESOURCE_HUB_SITE_NAMES.framehdr },
  { code: "dianying", label: RESOURCE_HUB_SITE_NAMES.dianying },
];

// 防止 http 模块未使用警告
const _http = http;
void _http;
</script>

<template>
  <div v-show="matches('资源站')">
    <CloudToolCard
      :enabled="draft.enabled"
      name="资源站"
      driver="聚合观影 / 聚影 / 帧影 / 癫影，转存到本地网盘"
      logo-text="资"
      :stat-value="String(enabledCount)"
      stat-label="已启用站点"
    >
      在「前台首页」聚合外部影视资源分享站的搜索结果，找到的夸克/115/光鸭分享链接可一键转存到本地网盘。
      <template #toggle>
        <button
          class="check-toggle"
          type="button"
          :class="{ on: draft.enabled }"
          :aria-label="draft.enabled ? '停用资源站' : '启用资源站'"
          :disabled="saving"
          title="启用 / 停用"
          @click="toggleEnabled"
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path
              d="M3.5 8.5 6.5 11.5 12.5 4.5"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
      </template>
      <template #actions>
        <AppButton size="sm" variant="secondary" :disabled="saving" @click="openConfig">配置</AppButton>
      </template>
    </CloudToolCard>

    <AppModal :open="configOpen" title="资源站 · 配置" size="lg" @close="closeConfig">
      <div class="rh-config">
        <p class="rh-config__tip">
          勾选要启用的站点，并填写对应账号密码（密码留空表示保留当前已保存的值）。配置保存后，前台首页即显示统一「资源站搜索」面板，可一键转存到本地网盘。
        </p>

        <div class="rh-sites">
          <div
            v-for="site in siteOptions"
            :key="site.code"
            class="rh-site"
            :class="{ 'is-on': draft.sites[site.code] }"
          >
            <label class="rh-site__head">
              <input
                type="checkbox"
                :checked="draft.sites[site.code]"
                @change="draft.sites[site.code] = ($event.target as HTMLInputElement).checked"
              />
              <span class="rh-site__name">{{ site.label }}</span>
              <span class="rh-site__code">{{ site.code }}</span>
            </label>

            <div class="rh-field">
              <label>站点地址</label>
              <input
                v-model.trim="draft[site.code].url"
                placeholder="例如 https://framehdr.com"
                spellcheck="false"
              />
            </div>

            <div class="rh-row">
              <div class="rh-field">
                <label>用户名 / 邮箱</label>
                <input
                  v-model.trim="draft[site.code].username"
                  placeholder="登录用户名"
                  spellcheck="false"
                  autocomplete="off"
                />
              </div>
              <div class="rh-field">
                <label>登录密码</label>
                <input
                  v-model="draft[site.code].password"
                  type="password"
                  autocomplete="new-password"
                  :placeholder="
                    cfg?.[site.code]?.password_configured
                      ? '留空保留当前已保存的密码'
                      : '可选'
                  "
                />
              </div>
            </div>

            <div v-if="site.code === 'framehdr'" class="rh-field">
              <label>登录 Cookie（可选）</label>
              <input
                v-model="draft[site.code].token"
                type="password"
                autocomplete="new-password"
                :placeholder="
                  cfg?.framehdr?.token_configured
                    ? '留空保留当前已保存的 Cookie'
                    : 'GeeTest 验证码不便解决时，可粘贴登录后的 Cookie'
                "
              />
            </div>

            <div v-if="site.code === 'guanying'" class="rh-field">
              <label>登录 Cookie（可选）</label>
              <input
                v-model="draft[site.code].cookie"
                type="password"
                autocomplete="new-password"
                :placeholder="
                  cfg?.guanying?.cookie_configured
                    ? '留空保留当前已保存的 Cookie'
                    : '浏览器 DevTools 复制任意观影请求 Cookie，跳过 PoW 与自动登录'
                "
              />
            </div>

            <div v-if="site.code === 'jying'" class="rh-field">
              <label>App Key（可选）</label>
              <input
                v-model="draft[site.code].appKey"
                type="password"
                autocomplete="new-password"
                :placeholder="
                  cfg?.jying?.app_key_configured
                    ? '留空保留当前已保存的 App-Key'
                    : '上游校验的 App-Key 请求头值，不填也能登录'
                "
              />
            </div>

            <div v-if="site.code === 'dianying'" class="rh-field">
              <label>OpenAPI Token（必填）</label>
              <input
                v-model="draft[site.code].token"
                type="password"
                autocomplete="new-password"
                :placeholder="
                  cfg?.dianying?.token_configured
                    ? '留空保留当前已保存的 Token'
                    : '癫影 VIP 的 OpenAPI Key，用于 Bearer 鉴权'
                "
              />
            </div>

            <div v-if="site.code === 'dianying'" class="rh-field">
              <label>登录 Cookie（可选）</label>
              <input
                v-model="draft[site.code].cookie"
                type="password"
                autocomplete="new-password"
                :placeholder="
                  cfg?.dianying?.cookie_configured
                    ? '留空保留当前已保存的 Cookie'
                    : '浏览器 DevTools 复制任意癫影请求 Cookie'
                "
              />
            </div>
          </div>
        </div>

        <div class="rh-test">
          <div class="rh-test__head">连通性测试（未保存的配置也可直接测试）</div>
          <div class="rh-test__row">
            <input v-model="testKw" placeholder="输入关键词，如：流浪地球" :disabled="testing" @keyup.enter="testSite('framehdr')" />
            <AppButton size="sm" variant="secondary" :disabled="testing || !testKw.trim()" @click="testSite('framehdr')">测试帧影</AppButton>
            <AppButton size="sm" variant="secondary" :disabled="testing || !testKw.trim()" @click="testSite('jying')">测试聚影</AppButton>
            <AppButton size="sm" variant="secondary" :disabled="testing || !testKw.trim()" @click="testSite('guanying')">测试观影</AppButton>
            <AppButton size="sm" variant="secondary" :disabled="testing || !testKw.trim()" @click="testSite('dianying')">测试癫影</AppButton>
          </div>
          <p v-if="testError" class="rh-test__error">{{ testError }}</p>
          <div v-if="testItems.length" class="rh-test__list">
            <div v-for="(it, i) in testItems" :key="`${it.url}-${i}`" class="rh-test__item">
              <span class="rh-test__plat">{{ it.platform || it.source }}</span>
              <strong>{{ it.title || "（未命名资源）" }}</strong>
              <small>{{ it.url }}</small>
            </div>
          </div>
        </div>
      </div>

      <template #footer>
        <span class="rh-config__foot">保存密码不会回显到列表，留空表示保留当前已保存的值。</span>
        <AppButton variant="secondary" :disabled="savingConfig" @click="closeConfig">取消</AppButton>
        <AppButton variant="primary" :disabled="savingConfig" @click="onSave">
          {{ savingConfig ? "保存中…" : "保存设置" }}
        </AppButton>
      </template>
    </AppModal>
  </div>
</template>

<style scoped>
.check-toggle {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  border: 0;
  padding: 0;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  background: var(--border);
  color: var(--text-muted);
  transition: background 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;
}
.check-toggle svg {
  width: 14px;
  height: 14px;
}
.check-toggle:hover {
  background: var(--surface-hover);
}
.check-toggle.on {
  background: var(--success);
  color: #fff;
  box-shadow: 0 0 0 4px rgba(16, 185, 129, 0.16);
}
.check-toggle.on:hover {
  background: color-mix(in srgb, var(--success) 88%, #000);
}
.check-toggle:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.rh-config {
  display: grid;
  gap: 16px;
}
.rh-config__tip {
  margin: 0;
  padding: 10px 12px;
  border-radius: 10px;
  background: var(--surface-sunken);
  border: 1px solid var(--border-soft);
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.6;
}
.rh-sites {
  display: grid;
  gap: 14px;
}
.rh-site {
  border: 1px solid var(--border-soft);
  border-radius: 12px;
  padding: 12px 14px;
  background: var(--surface);
  display: grid;
  gap: 10px;
}
.rh-site.is-on {
  border-color: color-mix(in srgb, var(--success) 50%, var(--border));
}
.rh-site__head {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}
.rh-site__code {
  font-size: 12px;
  color: var(--text-muted);
  font-weight: 400;
  margin-left: auto;
}
.rh-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}
.rh-field {
  display: grid;
  gap: 6px;
}
.rh-field label {
  font-size: 12px;
  color: var(--text-muted);
}
.rh-field input {
  padding: 9px 12px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--bg);
  color: var(--text);
  font-size: 13px;
  outline: none;
}
.rh-field input:focus {
  border-color: var(--primary);
}
.rh-test {
  border: 1px solid var(--border-soft);
  border-radius: 12px;
  padding: 12px;
  background: var(--surface-sunken);
  display: grid;
  gap: 10px;
}
.rh-test__head {
  font-size: 12px;
  color: var(--text-muted);
}
.rh-test__row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.rh-test__row input {
  flex: 1;
  min-width: 160px;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface);
  color: var(--text);
  font-size: 13px;
  outline: none;
}
.rh-test__error {
  margin: 0;
  color: #e5484d;
  font-size: 13px;
}
.rh-test__list {
  display: grid;
  gap: 8px;
}
.rh-test__item {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 10px;
  align-items: center;
  border: 1px solid var(--border-soft);
  background: var(--surface);
  border-radius: 10px;
  padding: 8px 10px;
}
.rh-test__plat {
  color: var(--primary);
  background: color-mix(in srgb, var(--primary) 10%, transparent);
  font-size: 12px;
  border-radius: 6px;
  padding: 2px 7px;
  white-space: nowrap;
}
.rh-test__item strong {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rh-test__item small {
  grid-column: 2;
  color: var(--text-muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rh-config__foot {
  margin-right: auto;
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.5;
}
@media (max-width: 560px) {
  .rh-row {
    grid-template-columns: 1fr;
  }
}
</style>