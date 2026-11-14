<script setup lang="ts">
import './draft-test.js';
import { t } from "./i18n";
import { ref, computed, onMounted, onUnmounted } from "vue";
import ReleaseNotes from "./ReleaseNotes.vue";
import BackupPanel from "./BackupPanel.vue";
import ActivityPanel from "./ActivityPanel.vue";
import AppearanceControls from "./AppearanceControls.vue";
import UiIcon from "./UiIcon.vue";
import type { User, Project, Release, Device, Notice, Log } from './types';
const user = ref<User | null>(null),
  ready = ref(false),
  busy = ref(false),
  message = ref(""),
  error = ref(""),
  section = ref("releases");
const hubConnected = ref(true);
async function checkConnection() {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 5000);
  try { hubConnected.value = (await fetch('/healthz', {cache:'no-store',signal:controller.signal})).ok; }
  catch { hubConnected.value = false; }
  finally { clearTimeout(timeout); }
}
const projects = ref<Project[]>([]),
  selected = ref<number | null>(null),
  releases = ref<Release[]>([]),
  devices = ref<Device[]>([]),
  logs = ref<Log[]>([]),
  members = ref<{ id: number; name: string; role: string }[]>([]),
  users = ref<User[]>([]);
const releaseQuery = ref("");
const releaseStatus = ref("all");
const filteredReleases = computed(() => {
 const query = releaseQuery.value.trim().toLocaleLowerCase();
 return releases.value.filter(r =>
  (releaseStatus.value === "all" || (releaseStatus.value === "published" ? r.published : !r.published)) &&
  (!query || [r.version, r.file_name, r.platform, r.arch, r.hardware].join(" ").toLocaleLowerCase().includes(query))
 );
});
const historyMore = ref(false);
const audits = ref<{ id: number; name: string; action: string; at: number }[]>(
  [],
);
const uploadMeta = ref({
  version: "",
  channel: "stable",
  platform: "any",
  arch: "any",
  hardware: "any",
  package_kind: "full",
  base_version: "",
  target_path: "",
});
const loginMFARequired = ref(false);
const loginName = ref("admin"),
  loginPassword = ref(""),
  loginOTP = ref(""),
  modal = ref(""),
  deviceName = ref(""),
  deviceToken = ref(""),
  uploadNotes = ref(""),
  manifestFile = ref<File | null>(null),
  artifactFile = ref<File | null>(null),
  uploadProgress = ref(0);
const passwordCurrent = ref(""), passwordNew = ref(""), passwordConfirm = ref(""), passwordOTP = ref("");
const mfaEnabled = ref(false),
  mfaPassword = ref(""),
  mfaCode = ref(""),
  mfaSecret = ref(""),
  mfaQR = ref("");
const freshProject = (): Project => ({
  id: 0,
  slug: "",
  name: "",
  description: "",
  public: false,
  telemetry: false,
  heartbeat: 60,
  timeout: 180,
  public_key: "",
  role: "owner",
  has_publish_password: false,
});
const noticeTab = ref('webhook');
const draft = ref<Project>(freshProject()),
  notice = ref<Notice>({
 webhook_enabled:true, telegram_enabled:true, mqtt_enabled:true,
 webhook_source:0, telegram_source:0, mqtt_source:0,

    webhook: "",
    webhook_secret: "",
    telegram_token: "",
    telegram_chat: "",
    mqtt_url: "",
    mqtt_user: "",
    mqtt_password: "",
    mqtt_topic: "",
    allow_private: false,
  }),
  globalTelemetry = ref(true);
const memberName = ref(""),
  memberRole = ref("viewer"),
  newName = ref(""),
  newPassword = ref("");
const publicSlug = new URLSearchParams(location.search).get('project'),
  publicProject = ref<{
    name: string;
    slug: string;
    description: string;
  } | null>(null);
const publicMode =
  !!publicSlug || new URLSearchParams(location.search).has("public");
const publicProjects = ref<
  { slug: string; name: string; description: string }[]
>([]);
const project = computed(() =>
  projects.value.find((p) => p.id === selected.value),
);
const owner = computed(() => project.value?.role === "owner"),
  write = computed(() => owner.value || project.value?.role === "maintainer"),
  publish = computed(() => owner.value || project.value?.role === "reviewer");
const online = computed(
  () => devices.value.filter((d) => d.online_state === "online").length,
);
const navs = [
  ["releases", "版本发布", "▣"],
  ["devices", "设备状态", "◉"],
  ["notifications", "更新通知", "↗"],
  ["logs", "运行记录", "≡"],
  ["members", "项目成员", "♧"],
  ["settings", "项目设置", "⚙"],
  ["account", "账号安全", "◇"],
];
const labels: Record<string, string> = {
  online: "在线",
  offline: "心跳超时",
  unknown: "未上报",
  disabled: "采集已关闭",
  revoked: "已撤销",
  idle: "待机",
  downloading: "下载中",
  verified: "验签通过",
  installing: "安装中",
  confirmed: "已启动确认",
  failed: "失败",
  pending: "待发送",
  delivered: "已送达",
  cancelled: "已取消",
  owner: "所有者",
  maintainer: "维护者",
  reviewer: "发布审核者",
  viewer: "查看者",
};
const label = (s: string) => t(labels[s] || s);
const date = (s: number) =>
  s ? new Date(s * 1000).toLocaleString() : "尚未上报";
const size = (n: number) =>
  n < 1024
    ? `${n} B`
    : n < 1048576
      ? `${(n / 1024).toFixed(1)} KiB`
      : `${(n / 1048576).toFixed(1)} MiB`;
async function api<T = unknown>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  if (!(options.body instanceof FormData))
    headers.set("Content-Type", "application/json");
  if (user.value) headers.set("X-CSRF-Token", user.value.csrf);
  const r = await fetch("/api/v1" + path, {
    ...options,
    headers,
    credentials: "same-origin",
  });
  const data = await r
    .json()
    .catch(() => ({ message: "服务返回异常，请查看运行记录" }));
  if (!r.ok) {
    if (data.error === 'mfa_required' || data.error === 'invalid_otp') loginMFARequired.value = true;
    if (r.status === 401 && user.value) user.value = null;
    throw new Error(data.message || `请求失败 (${r.status})`);
  }
  return data as T;
}
async function run(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  message.value = "";
  try {
    await fn();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function login() {
  await run(async () => {
    const r = await api<{ user: User }>("/session", {
      method: "POST",
      body: JSON.stringify({
        name: loginName.value,
        password: loginPassword.value,
        otp: loginOTP.value,
      }),
    });
    user.value = r.user;
    loginPassword.value = "";
    loginOTP.value = "";
    await loadProjects();
  });
}
async function logout() {
  await run(async () => {
    await api("/session", { method: "DELETE" });
    user.value = null;
    projects.value = [];
    devices.value = [];
    deviceToken.value = "";
    modal.value = "";
    passwordCurrent.value = passwordNew.value = passwordConfirm.value = passwordOTP.value = "";
    mfaPassword.value = mfaCode.value = mfaSecret.value = mfaQR.value = "";
  });
}
async function loadProjects() {
  projects.value = await api<Project[]>("/projects");
  if (!projects.value.some((p) => p.id === selected.value))
    selected.value = projects.value[0]?.id ?? null;
  const s = await api<{ telemetry: boolean }>("/settings");
  globalTelemetry.value = s.telemetry;
  await refresh();
}
async function refresh() {
  const p = project.value;
  if (!p) return;
  const id = p.id;
  const [rs, ds] = await Promise.all([
    api<Release[]>(`/projects/${id}/releases?limit=20`),
    api<Device[]>(`/projects/${id}/devices`),
  ]);
  if (selected.value !== id) return;
  releases.value = rs;
  historyMore.value = rs.length === 20;
  devices.value = ds;
  draft.value = { ...p };
}
async function choose(id: number) {
  if (busy.value) return;
  selected.value = id;
  releases.value = [];
  devices.value = [];
  logs.value = [];
  members.value = [];
  await run(async () => {
    await refresh();
    await loadSection();
  });
}
async function navigate(s: string) {
  if (busy.value) return;
  section.value = s;
  await run(loadSection);
}
async function refreshAll() {
  await loadProjects();
  await loadSection();
}
async function refreshDevices() {
  const id = selected.value;
  if (!id) return;
  const [ds, s, ps] = await Promise.all([
    api<Device[]>(`/projects/${id}/devices`),
    api<{ telemetry: boolean }>("/settings"),
    api<Project[]>("/projects"),
  ]);
  if (selected.value !== id || section.value !== "devices") return;
  devices.value = ds;
  globalTelemetry.value = s.telemetry;
  projects.value = ps;
}
async function olderReleases() {
  await run(async () => {
    const last = releases.value.at(-1);
    if (!last) return;
    if (publicSlug) {
      const page = await api<{ releases: Release[] }>(
        `/public/${encodeURIComponent(publicSlug)}?limit=20&before=${last.sequence}`,
      );
      releases.value.push(...page.releases);
      historyMore.value = page.releases.length === 20;
    } else {
      const page = await api<Release[]>(
        `/projects/${project.value!.id}/releases?limit=20&before=${last.sequence}`,
      );
      releases.value.push(...page);
      historyMore.value = page.length === 20;
    }
  });
}
async function loadSection() {
  if (section.value === "system" && user.value?.admin) {
    users.value = await api("/users");
    return;
  }
  if (section.value === "account") {
    const m = await api<{ enabled: boolean }>("/mfa");
    mfaEnabled.value = m.enabled;
    mfaPassword.value = "";
    mfaCode.value = "";
    mfaSecret.value = "";
    mfaQR.value = "";
    return;
  }
  const p = project.value;
  if (!p) return;
  switch (section.value) {
    case "notifications":
      if (owner.value)
        notice.value = await api<Notice>(`/projects/${p.id}/notifications`);
      break;
    case "logs":
      logs.value = await api<Log[]>(`/projects/${p.id}/logs`);
      audits.value = await api(`/projects/${p.id}/audit`);
      break;
    case "members":
      members.value = await api(`/projects/${p.id}/members`);
      break;
    case "system":
      if (user.value?.admin) users.value = await api("/users");
      break;
  }
}
async function createProject() {
  await run(async () => {
    const p = await api<Project>("/projects", {
      method: "POST",
      body: JSON.stringify(draft.value),
    });
    selected.value = p.id;
    modal.value = "";
    await loadProjects();
    message.value = "项目已创建，可以直接上传文件并填写版本。";
  });
}
async function saveProject() {
  await run(async () => {
    await api(`/projects/${project.value!.id}`, {
      method: "PUT",
      body: JSON.stringify(draft.value),
    });
    await loadProjects();
    message.value = "项目设置已保存";
  });
}
async function createDevice() {
  await run(async () => {
    const r = await api<{ token: string }>(
      `/projects/${project.value!.id}/devices`,
      { method: "POST", body: JSON.stringify({ name: deviceName.value }) },
    );
    deviceToken.value = r.token;
    deviceName.value = "";
    await refresh();
  });
}
async function revoke(d: Device) {
  if (
    !confirm(
      `撤销 ${d.name} 的专属访问凭据与上报权限？公开项目仍可匿名检查、下载。`,
    )
  )
    return;
  await run(async () => {
    await api(`/devices/${d.id}`, { method: "DELETE" });
    await refresh();
  });
}
async function clearStatus() {
  if (
    !confirm(
      "清除本项目已保存的版本与心跳信息？设备凭据不受影响。若仍开启采集，客户端可再次上报。",
    )
  )
    return;
  await run(async () => {
    await api(`/projects/${project.value!.id}/telemetry`, { method: "DELETE" });
    await refresh();
    message.value = "历史设备状态已清除";
  });
}
async function toggleRelease(r: Release) {
  if (
    !confirm(
      r.published
        ? "撤回后不再推荐此版本。已经下载或安装的副本不会被远程删除。"
        : "发布后将进入更新检测，并向已配置渠道发送提醒。确认发布？",
    )
  )
    return;
  let password = "";
  if (!r.published && project.value?.has_publish_password) {
    const value = prompt('请输入此项目的发布密码（不保存到浏览器）');
    if (value === null) return;
    password = value;
  }
  await run(async () => {
    await api(`/releases/${r.id}/publish`, {
      method: "PUT",
      body: JSON.stringify({
        published: !r.published,
        publish_password: password,
      }),
    });
    await refresh();
    message.value = r.published ? "版本已撤回" : "版本已发布，通知已入队";
  });
}
function fileChange(e: Event, kind: string) {
  const file = (e.target as HTMLInputElement).files?.[0] || null;
  if (kind === "manifest") manifestFile.value = file;
  else artifactFile.value = file;
}
async function upload() {
  await run(async () => {
    if (!artifactFile.value) throw new Error("请选择固件或软件文件");
    const data = new FormData();
    if (project.value!.public_key) {
      if (!manifestFile.value)
        throw new Error("该项目启用高级签名，请选择签名清单");
      if (manifestFile.value.size > 8192) throw new Error("签名清单过大");
      data.append(
        "manifest",
        JSON.stringify(JSON.parse(await manifestFile.value.text())),
      );
    } else {
      const meta = { ...uploadMeta.value };
      if (meta.package_kind === "full") {
        meta.base_version = "";
        meta.target_path = "";
      }
      data.append("metadata", JSON.stringify(meta));
    }
    data.append("notes", uploadNotes.value);
    data.append("file", artifactFile.value);
    uploadProgress.value = 0;
    await new Promise<void>((resolve, reject) => {
      const x = new XMLHttpRequest();
      x.open("POST", `/api/v1/projects/${project.value!.id}/releases`);
      x.setRequestHeader("X-CSRF-Token", user.value!.csrf);
      x.upload.onprogress = (e) => {
        if (e.lengthComputable)
          uploadProgress.value = Math.round((e.loaded / e.total) * 100);
      };
      x.onload = () => {
        if (x.status >= 200 && x.status < 300) resolve();
        else {
          let m = "上传失败";
          try {
            m = JSON.parse(x.responseText).message;
          } catch {}
          reject(new Error(m));
        }
      };
      x.onerror = () => reject(new Error("网络中断，请重新上传"));
      x.timeout = 30 * 60 * 1000;
      x.ontimeout = () => reject(new Error("上传超时"));
      x.send(data);
    });
    modal.value = "";
    manifestFile.value = null;
    artifactFile.value = null;
    uploadNotes.value = "";
    uploadMeta.value.version = "";
    await refresh();
    message.value = "文件已保存为草稿，发布操作将记录当前账号署名。";
  });
}
async function saveNotice() {
  await run(async () => {
    await api(`/projects/${project.value!.id}/notifications`, {
      method: "PUT",
      body: JSON.stringify(notice.value),
    });
    await loadSection();
    message.value = "通知设置已保存；发布版本时触发";
  });
}
async function testNotice(action = 'send') {
  await run(async () => {
    if (!project.value) throw new Error('请先选择项目');
    const pid = project.value.id;
    const result = await (window as any).IntegrationDraft.run(() => ({project:project.value?.id,channel:noticeTab.value,action,config:notice.value}), (draft:any) => api<{ result?: {chats?:string[]} }>(`/projects/${pid}/notifications/test`,{method:'POST',body:JSON.stringify({channel:draft.channel,action:draft.action,config:draft.config})}));
    message.value = '当前表单测试成功，配置未保存';
    if (action === 'detect' && !notice.value.telegram_chat.trim() && result.result?.chats?.length === 1) notice.value.telegram_chat = result.result.chats[0];
  });
}
async function saveMember() {
  await run(async () => {
    await api(`/projects/${project.value!.id}/members`, {
      method: "POST",
      body: JSON.stringify({ name: memberName.value, role: memberRole.value }),
    });
    memberName.value = "";
    await loadSection();
    message.value = "成员权限已更新";
  });
}
async function createUser() {
  await run(async () => {
    await api("/users", {
      method: "POST",
      body: JSON.stringify({
        name: newName.value,
        password: newPassword.value,
        admin: false,
      }),
    });
    newPassword.value = "";
    newName.value = "";
    users.value = await api("/users");
    message.value = "用户已创建";
  });
}
async function changePassword() {
  await run(async () => {
    if (passwordNew.value !== passwordConfirm.value) throw new Error("两次新密码不一致");
    await api("/password", {method:"POST", body:JSON.stringify({current_password:passwordCurrent.value,new_password:passwordNew.value,otp:passwordOTP.value})});
    passwordCurrent.value = passwordNew.value = passwordConfirm.value = passwordOTP.value = "";
    user.value = null; message.value = "密码已修改，请使用新密码重新登录";
  });
}
async function configureMFA(action: string) {
  await run(async () => {
    const result = await api<{ secret?: string; qr?: string }>("/mfa", {
      method: "POST",
      body: JSON.stringify({
        action,
        password: mfaPassword.value,
        code: mfaCode.value,
      }),
    });
    if (action === "begin" || action === "replace") {
      mfaCode.value = "";
      mfaSecret.value = result.secret || "";
      mfaQR.value = result.qr || "";
      message.value = "请扫描或手动添加后输入验证码确认";
    } else {
      mfaSecret.value = "";
      mfaQR.value = "";
      mfaPassword.value = "";
      mfaCode.value = "";
      user.value = null;
      message.value = "验证器设置已更新，请重新登录";
    }
  });
}
async function setGlobal() {
  await run(async () => {
    await api("/settings", {
      method: "PUT",
      body: JSON.stringify({ telemetry: globalTelemetry.value }),
    });
    await refresh();
    message.value = "全局状态采集设置已保存";
  });
}
async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    message.value = "已复制";
  } catch {
    message.value = "当前环境不支持剪贴板，请选中文本手动复制";
  }
}
let timer: ReturnType<typeof setInterval> | undefined;
onMounted(async () => {
  await run(async () => {
    if (publicMode && !publicSlug) {
      publicProjects.value = await api("/public");
      return;
    }
    if (publicSlug) {
      const p = await api<{
        project: { name: string; slug: string; description: string };
        releases: Release[];
      }>(`/public/${encodeURIComponent(publicSlug)}?limit=20`);
      publicProject.value = p.project;
      releases.value = p.releases;
      historyMore.value = p.releases.length === 20;
      return;
    }
    const s = await api<{ authenticated: boolean; user?: User }>("/session");
    if (s.authenticated && s.user) {
      user.value = s.user;
      await loadProjects();
    }
  });
  ready.value = true;
  timer = setInterval(() => {
    if (user.value && !document.hidden) void checkConnection();
    if (
      user.value &&
      section.value === "devices" &&
      !busy.value &&
      !document.hidden
    )
      refreshDevices().catch(() => {});
  }, 15000);
});
onUnmounted(() => {
  if (timer) clearInterval(timer);
});
</script>

<template>
  <div v-if="!ready" class="loading">{{ t('Firmware Hub · 正在连接') }}</div>
  <main v-else-if="publicMode" class="public-page">
    <div class="row"><a class="brand" href="/"><img src="/icon.svg" alt="" /> Firmware Hub</a><span class="spacer"></span><AppearanceControls /></div>
    <div class="row">
      <a href="/?public">{{ t('所有公开项目') }}</a><span class="spacer"></span
      ><a href="/">{{ t('管理登录 →') }}</a>
    </div>
    <header class="public-head">
      
      <h1>
        {{ publicSlug ? publicProject?.name || t('项目不可用') : t('公开版本中心') }}
      </h1>
      <p>{{ publicProject?.description }}</p>
    </header>
    <p v-if="error" class="alert error">{{ error }}</p>
    <template v-if="!publicSlug">
      <p v-if="!publicProjects.length && !error" class="empty">
        {{ t('暂时没有公开项目') }}
      </p>
      <article v-for="p in publicProjects" :key="p.slug" class="card">
        <h2>
          <a :href="'/?project=' + encodeURIComponent(p.slug)"
            >{{ p.name }} ↗</a
          >
        </h2>
        <p class="muted">{{ p.description }}</p>
      </article>
    </template>
    <p v-else-if="!releases.length && !error" class="empty">{{ t('暂时没有公开版本') }}</p>
    <article v-for="r in releases" :key="r.id" class="card release">
      <div class="row">
        <h2>{{ r.version }}</h2>
        <span class="badge">{{ r.channel }}</span><span class="muted">{{
          date(r.created)
        }}</span>
      </div>
      <ReleaseNotes :notes="r.notes" />
      <p class="muted">
        {{ r.package_kind === "file" ? t('按文件更新') : t('完整文件 / 整包') }} ·
        {{ r.file_name }} {{ t('· 发布者：') }}{{ r.published_by || t('历史记录')
        }}<span v-if="r.base_version">
          {{ t('· 适用旧版本') }} {{ r.base_version }} → {{ r.target_path }}</span
        >
      </p>
      <div class="row">
        <span>{{ r.platform }} / {{ r.arch }} / {{ r.hardware }}</span
        ><span class="muted">{{ size(r.size) }}</span
        ><a class="button primary" :href="r.download">{{ t('下载文件 ↓') }}</a>
      </div>
      <details>
        <summary>{{ t('完整性校验') }}</summary>
        <code>{{ r.sha256 }}</code>
        <p class="muted">{{ t('SHA-256 用于检查传输完整性，不是发布者数字签名。') }}</p>
      </details>
    </article>
    <button v-if="historyMore" :disabled="busy" @click="olderReleases">
      {{ t('加载更早版本') }}
    </button>
    <footer class="site-footer">© 2026 Firmware Hub contributors · MIT License</footer>
  </main>
  <main v-else-if="!user" class="login-page">
    <header class="hub-login-header">
      <a class="brand" href="/"><img src="/icon.svg" alt="" />Firmware Hub</a>
      <AppearanceControls :show-about="false" />
    </header>
    <section class="login-form">
      
      <h2>{{ t('登录工作空间') }}</h2>
      <p class="muted">{{ t('使用管理员或项目成员账号继续。') }}</p>
      <p><a href="/?public">{{ t('无需登录，浏览公开版本 →') }}</a></p>
      <form @submit.prevent="login">
        <label
          >{{ t('用户名') }}<input
            v-model="loginName" @input="loginMFARequired = false; loginOTP = ''"
            autocomplete="username"
            required /></label
        ><label
          >{{ t('密码') }}<input
            v-model="loginPassword" @input="loginMFARequired = false; loginOTP = ''"
            type="password"
            autocomplete="current-password"
            required
        /></label>
        <label v-if="loginMFARequired"
          >{{ t('验证器校验码') }}<input
            v-model="loginOTP"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="6"
            :placeholder="t('6 位校验码')"
        /></label>
        <p v-if="error" class="alert error" role="alert">{{ error }}</p>
        <button class="primary" :disabled="busy">
          {{ busy ? t('正在登录…') : t('登录 →') }}
        </button>
      </form>
      <footer class="login-copyright">© 2026 Firmware Hub contributors · MIT License</footer>
    </section>
  </main>
  <div v-else class="shell" data-drawer-host>
    <button class="drawer-backdrop" type="button" data-drawer-close :aria-label="t('关闭导航')"></button>
    <aside id="hub-nav" class="sidebar" data-drawer :aria-label="t('工作空间导航')">
      <a class="brand" href="/"><img src="/icon.svg" alt="" />Firmware Hub</a>
      <div class="space-label">{{ t('工作空间') }} <span>v0.6.0</span></div>
      <button
        class="new-project"
        @click="
          draft = freshProject();
          modal = 'project';
        "
      >
        {{ t('＋ 创建项目') }}</button
      ><label class="project-select"
        >{{ t('当前项目') }}<select
          :value="selected ?? ''"
          @change="choose(Number(($event.target as HTMLSelectElement).value))"
        >
          <option v-if="!projects.length" value="">{{ t('尚未创建项目') }}</option>
          <option v-for="p in projects" :key="p.id" :value="p.id">
            {{ p.name }}
          </option>
        </select></label
      >
      <nav>
        <button
          v-for="n in navs"
          :key="n[0]"
          :class="{ active: section === n[0] }"
          @click="navigate(n[0])"
        >
          <span class="nav-icon"><UiIcon :name="n[0]" /></span>{{ t(n[1]) }}<i v-if="section === n[0]"></i></button
        ><button
          v-if="user.admin"
          :class="{ active: section === 'system' }"
          @click="navigate('system')"
        >
          <span class="nav-icon"><UiIcon name="system" /></span>{{ t('系统管理') }}
        </button>
        <button type="button" data-about-open><span class="nav-icon"><UiIcon name="about" /></span>{{ t('关于') }}</button>
      </nav>
      <div class="sidebar-footer">
        <span class="avatar">{{ user.name[0]?.toUpperCase() }}</span>
        <div>
          <strong>{{ user.name }}</strong><small>{{
            user.admin ? t('平台管理员') : t('项目成员')
          }}</small>
        </div>
        <button class="account-signout" :title="t('退出登录')" :aria-label="t('退出登录')" @click="logout"><svg class="ui-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 4H4v16h5 M14 8l4 4-4 4 M8 12h12"/></svg></button>
      </div>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <button class="hub-nav-toggle" type="button" data-drawer-toggle aria-controls="hub-nav" aria-expanded="false" :aria-label="t('主导航')"><svg class="ui-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><path d="M4 6h16 M4 12h16 M4 18h16" /></svg></button>
        <span class="muted"
          >{{ t('工作空间 /') }} {{ project?.name || t('开始') }} /
          <b>{{
            t(navs.find((n) => n[0] === section)?.[1] || '系统管理')
          }}</b></span
        ><div class="row"><AppearanceControls :show-about="false" /><span class="top-status connection-indicator" :class="{ offline: !hubConnected }" role="status" :title="t(hubConnected ? 'Hub 运行中' : '连接中断')" :aria-label="t(hubConnected ? 'Hub 运行中' : '连接中断')"><i></i><span class="visually-hidden">{{ t(hubConnected ? 'Hub 运行中' : '连接中断') }}</span></span></div>
      </header>
      <div class="content">
        <div class="page-heading">
          <div>
            <span class="eyebrow">{{
              section === "devices" ? "DEVICE VISIBILITY" : "FIRMWARE WORKSPACE"
            }}</span>
            <h1>{{ t(navs.find((n) => n[0] === section)?.[1] || '系统管理') }}</h1>
            <p>
              {{
                section === "devices"
                  ? t('只采集你明确允许的状态，在线以最近一次心跳为准。')
                  : t('管理版本发布、设备状态与通知。')
              }}
            </p>
          </div>
          <div class="row">
            <button :disabled="busy" @click="run(refreshAll)">{{ t('↻ 刷新') }}</button
            ><button
              v-if="section === 'releases' && project && write"
              class="primary"
              @click="modal = 'release'"
            >
              {{ t('＋ 上传版本') }}</button
            ><button
              v-if="section === 'devices' && project && write"
              class="primary"
              @click="
                deviceToken = '';
                modal = 'device';
              "
            >
              {{ t('＋ 添加设备') }}
            </button>
          </div>
        </div>
        <p v-if="error" class="alert error" role="alert">{{ error }}</p>
        <p v-if="message" class="alert success" role="status">{{ message }}</p>
        <template v-if="section === 'system' && user.admin"
          ><BackupPanel base="/api/v1" :csrf="user.csrf" /><section class="card">
            <h2>{{ t('状态采集总开关') }}</h2>
            <p class="muted">
              {{ t('关闭后，所有项目拒收设备心跳；已保存信息保留，可在各项目清除。') }}
            </p>
            <label class="check"
              ><input
                v-model="globalTelemetry"
                type="checkbox"
              />{{ t('允许项目按需接收设备状态') }}</label
            ><button class="primary" :disabled="busy" @click="setGlobal">
              {{ t('保存设置') }}
            </button>
          </section>
          <section class="card">
            <h2>{{ t('用户管理') }}</h2>
            <form class="inline-form" @submit.prevent="createUser">
              <input
                v-model="newName"
                :placeholder="t('用户名')"
                required
                autocomplete="off"
              /><input
                v-model="newPassword"
                type="password"
                minlength="12"
                maxlength="72"
                :placeholder="t('初始密码（至少 12 字节）')"
                required
                autocomplete="new-password"
              /><button :disabled="busy">{{ t('创建普通用户') }}</button>
            </form>
            <div v-for="u in users" :key="u.id" class="list-row">
              <strong>{{ u.name }}</strong><span class="muted">{{
                u.admin ? t('管理员') : t('普通用户')
              }}</span>
            </div>
          </section>
          <ActivityPanel :request="api" :all="true"
        /></template>
        <template v-else-if="section === 'account'"
          ><section class="card"><h2>{{ t('修改密码') }}</h2><p class="muted">{{ t('修改后旧密码与所有设备的登录状态立即失效，请使用新密码重新登录。') }}</p>
<form @submit.prevent="changePassword"><label>{{ t('当前密码') }}<input v-model="passwordCurrent" type="password" autocomplete="current-password" maxlength="72" required /></label><label>{{ t('新密码（12–72 字节）') }}<input v-model="passwordNew" type="password" autocomplete="new-password" maxlength="72" required /></label><label>{{ t('确认新密码') }}<input v-model="passwordConfirm" type="password" autocomplete="new-password" maxlength="72" required /></label><label v-if="mfaEnabled">{{ t('当前验证码') }}<input v-model="passwordOTP" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required /></label><button class="primary" :disabled="busy">{{ t('修改密码并重新登录') }}</button></form></section><ActivityPanel :request="api" />
          <section class="card">
            <h2>{{ t('验证器二次验证') }}</h2>
            <p class="muted">
              {{ t('保护网页登录，不影响设备凭据和固件下载。支持常见 TOTP\n              验证器；启用后请保存好验证器备份。') }}
            </p>
            <span class="badge" :class="mfaEnabled ? 'green' : 'gray'">{{
              mfaEnabled ? t('已启用') : t('尚未启用')
            }}</span>
            <form
              @submit.prevent="
                configureMFA(
                  mfaSecret ? 'confirm' : mfaEnabled ? 'replace' : 'begin',
                )
              "
            >
              <label
                >{{ t('当前登录密码') }}<input
                  v-model="mfaPassword"
                  type="password"
                  required
                  autocomplete="current-password"
              /></label>
              <template v-if="mfaSecret"
                ><p class="hint">
                  {{ t('请用验证器扫描二维码，或手动输入密钥。确认前不会启用。') }}
                </p>
                <img
                  :src="mfaQR"
                  width="220"
                  height="220"
                  :alt="t('验证器绑定二维码')"
                /><code class="token">{{ mfaSecret }}</code></template
              >
              <label v-if="mfaEnabled || mfaSecret"
                >{{ t('验证器当前校验码') }}<input
                  v-model="mfaCode"
                  inputmode="numeric"
                  autocomplete="one-time-code"
                  pattern="[0-9]{6}"
                  maxlength="6"
                  required
              /></label>
              <button class="primary" :disabled="busy">
                {{
                  mfaSecret ? t('确认绑定') : mfaEnabled ? t('更换验证器') : t('添加验证器')
                }}
              </button>
            <button v-if="mfaEnabled && !mfaSecret" type="button" class="secondary" :disabled="busy" @click="configureMFA('disable')">{{ t('验证并关闭') }}</button></form>
            <p class="muted footnote">
              {{ t('新验证器确认前，原验证器继续有效。确认后旧验证器与所有设备的登录状态立即失效。') }}
            </p>
          </section></template
        >
        <section v-else-if="!project" class="card empty">
          <div class="empty-icon">▣</div>
          <h2>{{ t('从一个项目开始') }}</h2>
          <p>
            {{ t('创建项目后上传文件、填写版本与更新说明。默认按账号记录发布责任；数字签名可在项目设置中按需启用。') }}
          </p>
          <button
            class="primary"
            @click="
              draft = freshProject();
              modal = 'project';
            "
          >
            {{ t('创建第一个项目') }}</button
          ><small class="muted">{{ t('普通账号发布无需安装 fwtool。') }}</small>
        </section>
        <template v-else>
          <div
            v-if="section === 'releases' || section === 'devices'"
            class="stats"
          >
            <article>
              <span>{{
                section === "devices" ? t('已加载设备') : t('已加载版本')
              }}</span><strong>{{
                section === "devices" ? devices.length : releases.length
              }}</strong
              ><small>{{ project.slug }}</small>
            </article>
            <article>
              <span>{{
                section === "devices" ? t('加载设备在线') : t('其中已发布')
              }}</span><strong>{{
                section === "devices"
                  ? online
                  : releases.filter((r) => r.published).length
              }}</strong
              ><small>{{
                section === "devices"
                  ? t('不是持续连通性保证')
                  : t('按账号记录发布责任')
              }}</small>
            </article>
            <article>
              <span>{{ t('设备状态通道') }}</span
              ><strong class="stat-text">{{
                project.telemetry && globalTelemetry ? t('按需开启') : t('已关闭')
              }}</strong
              ><small>{{ t('客户端仍可独立关闭') }}</small>
            </article>
          </div>
          <section v-if="section === 'releases'" class="card">
            <div class="card-heading">
              <h2>
                {{ t('发布历史') }} <span class="count">{{ releases.length }}</span>
              </h2>
              <a
                v-if="project.public"
                class="text-link"
                :href="'/?project=' + project.slug"
                target="_blank"
                rel="noopener noreferrer"
                >{{ t('查看公开发布页 ↗') }}</a
              >
            </div>
            <div v-if="!releases.length" class="empty">
              <div class="empty-icon">↑</div>
              <h3>{{ t('还没有发布版本') }}</h3>
              <p>{{ t('上传固件、软件整包或单个文件，填写版本说明后发布。') }}</p>
            </div>
            <div class="release-toolbar">
              <label>{{ t('搜索已加载版本') }}<input v-model="releaseQuery" type="search" :placeholder="t('版本、文件、平台或型号')" /></label>
              <label>{{ t('发布状态') }}<select v-model="releaseStatus"><option value="all">{{ t('全部状态') }}</option><option value="published">{{ t('已发布') }}</option><option value="draft">{{ t('草稿 / 已撤回') }}</option></select></label>
              <p class="muted">{{ t('显示') }} {{ filteredReleases.length }} {{ t('/ 已加载') }} {{ releases.length }} {{ t('条；更早记录请继续加载。') }}</p>
            </div>
            <p v-if="releases.length && !filteredReleases.length" class="muted" role="status">{{ t('已加载版本中没有匹配项。可调整筛选或加载更早版本。') }}</p>
            <article v-for="r in filteredReleases" :key="r.id" class="release">
              <div class="row">
                <span class="version-mark">↥</span><h3>{{ r.version }}</h3>
                <span class="badge">{{ r.channel }}</span>
                <span class="badge" :class="r.published ? 'green' : 'gray'">{{ r.published ? t('已发布') : t('草稿 / 已撤回') }}</span>
                <span class="spacer"></span><small class="muted">{{ date(r.created) }}</small>
              </div>
              <p class="muted">{{ r.platform }} / {{ r.arch }} · {{ r.hardware }} · {{ size(r.size) }} {{ t('· 发布序号') }} {{ r.sequence }}</p>
              <ReleaseNotes :notes="r.notes" />
              <p class="muted">{{ r.package_kind === 'file' ? t('按文件更新') : t('完整文件 / 整包') }} · {{ r.file_name }}
                <span v-if="r.base_version"> · {{ r.base_version }} → {{ r.target_path }}</span>
                <br />{{ t('上传：') }}{{ r.uploaded_by }} {{ t('· 发布：') }}{{ r.published_by || t('尚未发布') }}
                <span v-if="r.published_at"> · {{ date(r.published_at) }}</span>
              </p>
              <div class="row">
                <a class="button" :href="r.download">{{ t('下载文件') }} ↓</a>
                <button v-if="publish" :disabled="busy" @click="toggleRelease(r)">{{ r.published ? t('撤回版本') : t('发布版本') }}</button>
                <details><summary>{{ t('校验信息') }}</summary><code>SHA-256: {{ r.sha256 }}</code><small>{{ t('安全版本计数：') }}{{ r.security }}</small></details>
              </div>
            </article>
            <button v-if="historyMore" :disabled="busy" @click="olderReleases">
              {{ t('加载更早版本') }}
            </button>
          </section>
          <section v-else-if="section === 'devices'" class="card">
            <div class="card-heading">
              <h2>{{ t('客户端状态') }}</h2>
              <button v-if="write" :disabled="busy" @click="clearStatus">
                {{ t('清除已保存状态') }}
              </button>
            </div>
            <p v-if="!project.telemetry || !globalTelemetry" class="hint">
              {{ t('状态采集已关闭，设备仍可正常检查和下载固件。开启项目和全局开关后，还需客户端主动同意上报。') }}
            </p>
            <p class="muted">
              {{ t('心跳建议') }} {{ project.heartbeat }} {{ t('秒 · 超过') }}
              {{ project.timeout }} {{ t('秒未收到心跳显示超时 · 每 15 秒刷新本页') }}
            </p>
            <div v-if="!devices.length" class="empty">
              <h3>{{ t('尚未登记设备') }}</h3>
              <p>{{ t('为每台设备分配独立凭据，避免设备之间互相覆盖状态。') }}</p>
            </div>
            <div v-else class="table-wrap mobile-records">
              <table class="devices-table">
                <thead>
                  <tr>
                    <th>{{ t('设备') }}</th>
                    <th>{{ t('在线状态') }}</th>
                    <th>{{ t('最后上报版本') }}</th>
                    <th>{{ t('升级状态') }}</th>
                    <th>{{ t('最近心跳') }}</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="d in devices" :key="d.id">
                    <td :data-label="t('设备')">
                      <strong>{{ d.name }}</strong><small
                        >{{ d.platform || "—" }} / {{ d.arch || "—" }} /
                        {{ d.hardware || "—" }}</small
                      >
                    </td>
                    <td :data-label="t('在线状态')">
                      <span
                        class="badge"
                        :class="d.online_state === 'online' ? 'green' : 'gray'"
                        >{{ label(d.online_state) }}</span
                      >
                    </td>
                    <td :data-label="t('最后上报版本')">
                      <code>{{ d.version || "—" }}</code>
                    </td>
                    <td :data-label="t('升级状态')">{{ label(d.status) }}</td>
                    <td :data-label="t('最近心跳')">{{ date(d.last_seen) }}</td>
                    <td :data-label="t('信息')">
                      <button
                        v-if="write && !d.revoked"
                        class="danger-text"
                        :disabled="busy"
                        @click="revoke(d)"
                      >
                        {{ t('撤销凭据') }}
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p class="muted footnote">
              {{ t('状态由客户端自报，不等于硬件可信证明。关闭客户端上报后，已有记录会在超时后显示为心跳超时。') }}
            </p>
          </section>
          <section v-else-if="section === 'notifications'" class="card notice-settings">
            <h2>{{ t('更新通知设置') }}</h2>
            <p class="muted">{{ t('Webhook、Telegram 和 MQTT 可独立启用。共用连接的项目可使用相同配置，发送开关互不影响。') }}</p>
            <p v-if="!owner" class="hint">{{ t('只有项目所有者可以管理通知凭据。') }}</p>
            <form v-else @submit.prevent="saveNotice">
              <div class="notice-tabs" role="tablist" aria-label="通知渠道">
                <button v-for="c in [{id:'webhook',name:'Webhook'},{id:'mqtt',name:'MQTT'}]" :key="c.id" type="button" role="tab" :aria-selected="noticeTab === c.id" :class="{active:noticeTab === c.id}" @click="noticeTab=c.id">{{ c.name }}</button>
              </div>
<section v-show="noticeTab === 'webhook'" class="notice-channel" role="tabpanel">
 <label class="check"><input type="checkbox" v-model="notice.webhook_enabled" />{{ t('启用 Webhook 通知') }}</label>
 <label>{{ t('连接配置') }}<select v-model.number="notice.webhook_source"><option :value="0">{{ t('本项目独立配置') }}</option><option v-for="p in projects.filter(p => p.id !== project?.id && p.role === 'owner')" :key="p.id" :value="p.id">{{ t('共用：') }}{{ p.name }}</option></select></label>
 <p v-if="notice.webhook_source" class="hint">{{ t('引用所选项目的连接信息，后续修改会同步生效；凭据不会显示到当前项目。仅可引用同一所有者的项目，不能循环引用。') }}</p>
 <div v-else class="form-grid"><label>Webhook 地址<input v-model="notice.webhook" placeholder="https://example.com/webhook" /></label><label>{{ t('Webhook 签名密钥') }}<input v-model="notice.webhook_secret" type="password" autocomplete="new-password" :placeholder="notice.has_webhook_secret ? t('已保存，留空不变') : ''" /></label></div>
</section>
<section v-show="noticeTab === 'mqtt'" class="notice-channel" role="tabpanel">
 <label class="check"><input type="checkbox" v-model="notice.mqtt_enabled" />{{ t('启用 MQTT 通知') }}</label>
 <label>{{ t('连接配置') }}<select v-model.number="notice.mqtt_source"><option :value="0">{{ t('本项目独立配置') }}</option><option v-for="p in projects.filter(p => p.id !== project?.id && p.role === 'owner')" :key="p.id" :value="p.id">{{ t('共用：') }}{{ p.name }}</option></select></label>
 <p v-if="notice.mqtt_source" class="hint">{{ t('引用所选项目的连接信息，后续修改会同步生效；凭据不会显示到当前项目。仅可引用同一所有者的项目，不能循环引用。') }}</p>
 <div v-else class="form-grid"><label>MQTT Broker<input v-model="notice.mqtt_url" placeholder="ssl://broker.example.com:8883" /></label><label>{{ t('MQTT 用户名') }}<input v-model="notice.mqtt_user" autocomplete="off" /></label><label>{{ t('MQTT 密码') }}<input v-model="notice.mqtt_password" type="password" autocomplete="new-password" :placeholder="notice.has_mqtt_password ? t('已保存，留空不变') : ''" /></label></div>
<label>{{ t('MQTT 主题 / 项目覆盖') }}<input v-model="notice.mqtt_topic" placeholder="firmware/{project}/available" /><small>{{ t('支持 {project} 自动替换为当前项目标识。共用连接时留空沿用来源主题模板，也可以指定本项目主题。') }}</small></label></section>
<p class="hint">{{ t('凭据留空保留原值，填写单个 - 清除。关闭渠道会保留配置。') }}</p>
 <label v-if="user.admin" class="check"><input v-model="notice.allow_private" type="checkbox" />{{ t('允许本项目独立连接访问内网 HTTP / TCP（仅可信目标）') }}</label>
 <button class="primary" :disabled="busy">{{ t('保存通知配置') }}</button> <button type="button" class="secondary" :disabled="busy" @click="testNotice()">{{ t('测试当前渠道') }}</button><p class="hint">{{ t('测试使用当前表单，不会保存或启用配置；密钥留空沿用已保存的密钥。') }}</p>
 </form></section>
          <section v-else-if="section === 'logs'" class="card">
            <h2>{{ t('项目操作记录') }}</h2>
            <p class="muted">
              {{ t('记录操作账号、版本发布/撤回与设置变更；最近 200\n              条。账号署名不是数字签名。') }}
            </p>
            <div v-for="a in audits" :key="a.id" class="list-row">
              <div>
                <strong>{{ a.name }}</strong>
                <p>{{ a.action }}</p>
                <small>{{ date(a.at) }}</small>
              </div>
            </div>
            <h2>{{ t('通知投递记录') }}</h2>
            <div v-if="!logs.length" class="empty">
              {{ t('暂无投递记录。发布版本并配置渠道后会在这里显示。') }}
            </div>
            <div v-for="l in logs" :key="l.id" class="list-row">
              <div>
                <strong>{{ l.kind }}</strong><small
                  >{{ date(l.created) }} {{ t('· 已尝试') }} {{ l.attempts }} {{ t('次') }}</small
                >
                <p v-if="l.error" class="danger-text">{{ l.error }}</p>
              </div>
              <span
                class="badge"
                :class="l.state === 'delivered' ? 'green' : 'gray'"
                >{{ label(l.state) }}</span
              >
            </div>
          </section>
          <section v-else-if="section === 'members'" class="card">
            <h2>{{ t('项目成员') }}</h2>
            <p class="muted">
              {{ t('维护者负责上传，审核者负责发布。所有者可同时完成两项操作。') }}
            </p>
            <form v-if="owner" class="inline-form" @submit.prevent="saveMember">
              <input
                v-model="memberName"
                :placeholder="t('已存在的用户名')"
                required
              /><select v-model="memberRole">
                <option value="viewer">{{ t('查看者') }}</option>
                <option value="maintainer">{{ t('维护者') }}</option>
                <option value="reviewer">{{ t('发布审核者') }}</option>
                <option value="remove">{{ t('移除成员') }}</option></select
              ><button :disabled="busy">{{ t('更新成员') }}</button>
            </form>
            <div v-for="m in members" :key="m.id" class="list-row">
              <strong>{{ m.name }}</strong><span class="badge gray">{{
                label(m.role)
              }}</span>
            </div>
          </section>
          <section v-else-if="section === 'settings'" class="card">
            <h2>{{ t('项目设置') }}</h2>
            <form @submit.prevent="saveProject">
              <div class="form-grid">
                <label
                  >{{ t('项目名称') }}<input
                    v-model="draft.name"
                    :disabled="!owner"
                    required
                    maxlength="100" /></label
                ><label>{{ t('项目标识') }}<input :value="draft.slug" disabled /></label>
              </div>
              <label
                >{{ t('项目介绍') }}<textarea
                  v-model="draft.description"
                  :disabled="!owner"
                  rows="3"
                  maxlength="4000"
                ></textarea></label
              ><label class="check"
                ><input
                  v-model="draft.public"
                  type="checkbox"
                  :disabled="!owner"
                />{{ t('公开发布页和已发布文件（设备状态始终不公开）') }}</label
              >
              <hr />
              <h3>{{ t('可选：设备状态通道') }}</h3>
              <label class="check"
                ><input
                  v-model="draft.telemetry"
                  type="checkbox"
                  :disabled="!owner"
                />{{ t('允许接收客户端当前版本、升级状态与心跳') }}</label
              >
              <p class="muted">
                {{ t('默认关闭；不会采集 IP、用户名或设备序列号。Hub\n                总开关与客户端开关也必须允许。') }}
              </p>
              <div class="form-grid">
                <label
                  >{{ t('建议心跳间隔（秒）') }}<input
                    v-model.number="draft.heartbeat"
                    type="number"
                    min="15"
                    max="86400"
                    :disabled="!owner" /></label
                ><label
                  >{{ t('离线判定超时（秒）') }}<input
                    v-model.number="draft.timeout"
                    type="number"
                    :min="draft.heartbeat * 2"
                    max="604800"
                    :disabled="!owner"
                /></label>
              </div>
              <hr />
              <h3>{{ t('发布保护') }}</h3>
              <p class="muted">
                {{ t('当前：') }}{{
                  draft.public_key
                    ? t('高级数字签名模式')
                    : t('账号发布模式，无需 fwtool')
                }}{{ t('。账号模式信任服务器与账号权限，不能抵御服务器被入侵后替换更新。') }}
              </p>
              <label
                >{{ t('高级签名公钥') }}<input
                  :value="draft.public_key"
                  readonly
                  class="mono"
              /></label>
              <p class="muted">
                {{ t('保留高级模式以兼容已有客户端；默认新项目不需要公钥或签名工具。') }}
              </p>
              <button
                v-if="owner && draft.public_key"
                type="button"
                @click="draft.public_key = ''"
              >
                {{ t('改为账号发布模式') }}
              </button>
              <label v-if="owner"
                >{{ t('项目发布密码（可选，留空关闭；不修改则不要输入）') }}<input
                  v-model="draft.publish_password"
                  type="password"
                  autocomplete="new-password"
                  :placeholder="
                    draft.has_publish_password
                      ? t('已启用，未修改则保持')
                      : t('未启用')
                  "
              /></label>
              <label
                v-if="
                  owner &&
                  (draft.publish_password !== undefined ||
                    draft.public_key !== project.public_key)
                "
                >{{ t('验证当前账号密码') }}<input
                  v-model="draft.account_password"
                  type="password"
                  autocomplete="current-password"
                  required
              /></label>
              <button v-if="owner" class="primary" :disabled="busy">
                {{ t('保存项目设置') }}
              </button>
            </form>
          </section>
        </template>
      </div>
      <footer class="site-footer">
        © 2026 Firmware Hub contributors · MIT License  <span>{{ t('账号署名 / 按需连接') }}</span>
      </footer>
    </div>
  </div>
  <div v-if="modal" class="modal-backdrop" @click.self="!busy && (modal = '')">
    <section
      class="modal"
      role="dialog"
      aria-modal="true"
      :aria-label="
        modal === 'project'
          ? t('创建项目')
          : modal === 'release'
            ? t('上传版本')
            : t('添加设备')
      "
    >
      <div class="card-heading">
        <h2>
          {{
            modal === "project"
              ? t('创建项目')
              : modal === "release"
                ? t('上传版本')
                : t('添加设备')
          }}
        </h2>
        <button
          :disabled="busy"
          :title="t('关闭')"
          @click="
            modal = '';
            deviceToken = '';
          "
        >
          ×
        </button>
      </div>
      <p v-if="error" class="alert error">{{ error }}</p>
      <form v-if="modal === 'project'" @submit.prevent="createProject">
        <div class="form-grid">
          <label
            >{{ t('项目名称') }}<input
              v-model="draft.name"
              required
              maxlength="100"
              :placeholder="t('例如：设备控制器')" /></label
          ><label
            >{{ t('项目标识') }}<input
              v-model="draft.slug"
              required
              pattern="[A-Za-z0-9][A-Za-z0-9._+\-]{0,63}"
              placeholder="device-controller"
          /></label>
        </div>
        <label
          >{{ t('介绍') }}<textarea v-model="draft.description" rows="2"></textarea>
        </label>
        <details>
          <summary>{{ t('高级：独立数字签名（可选）') }}</summary>
          <label
            >{{ t('Ed25519 公钥（64 位十六进制）') }}<input
              v-model="draft.public_key"
              pattern="[0-9a-fA-F]{64}"
              class="mono"
              :placeholder="t('仅填写公钥，绝不上传私钥')"
          /></label>
          <p class="hint">
            {{ t('仅高级模式需要在本地运行') }}
            <code>fwtool keygen --out publisher.key</code>{{ t('。私钥留在发布者本地。') }}
          </p>
        </details>
        <label
          >{{ t('项目发布密码（可选，12–72 字节）') }}<input
            v-model="draft.publish_password"
            type="password"
            autocomplete="new-password"
            :placeholder="t('留空不启用')"
        /></label>
        <label class="check"
          ><input
            v-model="draft.telemetry"
            type="checkbox"
          />{{ t('启用可选的设备状态通道') }}</label
        ><label class="check"
          ><input
            v-model="draft.public"
            type="checkbox"
          />{{ t('公开已发布版本与下载') }}</label
        ><button class="primary" :disabled="busy">{{ t('创建项目') }}</button>
      </form>
      <form v-else-if="modal === 'release'" @submit.prevent="upload">
        <p class="muted">
          {{ t('上传后保存为草稿；发布时记录操作账号，不自动安装到客户端。') }}
        </p>
        <div v-if="!project?.public_key" class="form-grid">
          <label
            >{{ t('版本号') }}<input
              v-model="uploadMeta.version"
              required
              pattern="[A-Za-z0-9][A-Za-z0-9._+\-]{0,63}"
              placeholder="1.2.0"
          /></label>
          <label
            >{{ t('更新方式') }}<select v-model="uploadMeta.package_kind">
              <option value="full">{{ t('完整文件 / 软件整包') }}</option>
              <option value="file">{{ t('按文件更新（DLL / EXE 等）') }}</option>
            </select></label
          >
          <template v-if="uploadMeta.package_kind === 'file'"
            ><label
              >{{ t('适用旧版本') }}<input
                v-model="uploadMeta.base_version"
                required
                placeholder="1.1.0" /></label
            ><label
              >{{ t('软件目录内目标路径') }}<input
                v-model="uploadMeta.target_path"
                required
                :placeholder="t('MyApp.exe 或 plugins/module.dll')" /></label
          ></template>
        </div>
        <p v-if="!project?.public_key" class="hint">
          {{ t('整包可上传 windeployqt 生成目录的 ZIP（文件直接位于 ZIP\n          根目录）；也可直接上传设备\n          BIN。按文件更新只替换指定文件，不是二进制差分；仅向适用旧版本提供。') }}
        </p>
        <details v-if="!project?.public_key">
          <summary>{{ t('通道与兼容范围（可保持默认）') }}</summary>
          <div class="form-grid">
            <label>{{ t('通道') }}<input v-model="uploadMeta.channel" required /></label
            ><label>{{ t('平台') }}<input v-model="uploadMeta.platform" required /></label
            ><label>{{ t('架构') }}<input v-model="uploadMeta.arch" required /></label
            ><label
              >{{ t('硬件型号') }}<input v-model="uploadMeta.hardware" required
            /></label>
          </div>
        </details>
        <label v-if="project?.public_key"
          >{{ t('签名清单 manifest.json') }}<input
            type="file"
            accept=".json"
            required
            @change="fileChange($event, 'manifest')" /></label
        ><label
          >{{ t('对应固件 / 软件文件') }}<input
            type="file"
            required
            @change="fileChange($event, 'artifact')" /></label
        ><label
          >{{ t('版本说明（Markdown）') }}<textarea
            v-model="uploadNotes"
            rows="6"
            maxlength="32768"
            :placeholder="t('## 更新内容\n- 新增功能\n\n## 升级注意事项\n兼容型号与已知问题…')"
          ></textarea>
        </label>
        <details class="editor-preview">
          <summary>{{ t('预览版本说明') }}</summary>
          <ReleaseNotes :notes="uploadNotes" />
        </details>
        <progress v-if="busy" :value="uploadProgress" max="100"></progress
        ><button class="primary" :disabled="busy">
          {{ busy ? `上传并校验 ${uploadProgress}%` : t('保存草稿') }}
        </button>
      </form>
      <template v-else
        ><form v-if="!deviceToken" @submit.prevent="createDevice">
          <p class="muted">
            {{ t('每台设备使用独立凭据。设备名称由你指定，不自动推断用户名或地址。') }}
          </p>
          <label
            >{{ t('设备名称') }}<input
              v-model="deviceName"
              maxlength="100"
              :placeholder="t('例如：测试终端 01')"
              required /></label
          ><button class="primary" :disabled="busy">{{ t('创建访问凭据') }}</button>
        </form>
        <div v-else>
          <p class="hint">
            {{ t('凭据仅显示这一次。可用于本项目更新检测、下载和可选心跳。') }}
          </p>
          <code class="token">{{ deviceToken }}</code><button
            @click="copy(deviceToken)"
          >
            {{ t('复制凭据') }}
          </button>
          <p class="muted">{{ t('不要写进公开仓库、截图或公开发布页。') }}</p>
        </div></template
      >
    </section>
  </div>
</template>
