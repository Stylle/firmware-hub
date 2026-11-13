<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from "vue";
import ReleaseNotes from "./ReleaseNotes.vue";
import ActivityPanel from "./ActivityPanel.vue";
import AppearanceControls from "./AppearanceControls.vue";
type User = { id: number; name: string; admin: boolean; csrf: string };
type Project = {
  id: number;
  slug: string;
  name: string;
  description: string;
  public: boolean;
  telemetry: boolean;
  heartbeat: number;
  timeout: number;
  public_key: string;
  role: string;
  has_publish_password: boolean;
  publish_password?: string;
  account_password?: string;
};
type Release = {
  id: number;
  version: string;
  sequence: number;
  security: number;
  channel: string;
  platform: string;
  arch: string;
  hardware: string;
  sha256: string;
  size: number;
  notes: string;
  published: boolean;
  created: number;
  download: string;
  file_name: string;
  package_kind: string;
  base_version: string;
  target_path: string;
  uploaded_by: string;
  published_by: string;
  published_at: number;
};
type Device = {
  id: number;
  name: string;
  version: string;
  platform: string;
  arch: string;
  hardware: string;
  status: string;
  last_seen: number;
  online_state: string;
  revoked: boolean;
};
type Notice = {
  webhook: string;
  webhook_secret: string;
  telegram_token: string;
  telegram_chat: string;
  mqtt_url: string;
  mqtt_user: string;
  mqtt_password: string;
  mqtt_topic: string;
  allow_private: boolean;
  has_webhook_secret?: boolean;
  has_telegram_token?: boolean;
  has_mqtt_password?: boolean;
};
type Log = {
  id: number;
  kind: string;
  state: string;
  attempts: number;
  error: string;
  created: number;
};
const user = ref<User | null>(null),
  ready = ref(false),
  busy = ref(false),
  message = ref(""),
  error = ref(""),
  section = ref("releases");
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
const draft = ref<Project>(freshProject()),
  notice = ref<Notice>({
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
const publicSlug = new URLSearchParams(location.search).get("project"),
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
const label = (s: string) => labels[s] || s;
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
    const value = prompt("请输入此项目的发布密码（不保存到浏览器）");
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
    if (action === "begin") {
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
  <div v-if="!ready" class="loading">Firmware Hub · 正在连接</div>
  <main v-else-if="publicMode" class="public-page">
    <div class="row"><a class="brand" href="/"><img src="/icon.svg" alt="" /> Firmware Hub</a><span class="spacer"></span><AppearanceControls /></div>
    <div class="row">
      <a href="/?public">所有公开项目</a><span class="spacer"></span
      ><a href="/">管理登录 →</a>
    </div>
    <header class="public-head">
      <span class="eyebrow">RELEASE CENTER</span>
      <h1>
        {{ publicSlug ? publicProject?.name || "项目不可用" : "公开版本中心" }}
      </h1>
      <p>{{ publicProject?.description }}</p>
    </header>
    <p v-if="error" class="alert error">{{ error }}</p>
    <template v-if="!publicSlug">
      <p v-if="!publicProjects.length && !error" class="empty">
        暂时没有公开项目
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
    <p v-else-if="!releases.length && !error" class="empty">暂时没有公开版本</p>
    <article v-for="r in releases" :key="r.id" class="card release">
      <div class="row">
        <h2>{{ r.version }}</h2>
        <span class="badge">{{ r.channel }}</span><span class="muted">{{
          date(r.created)
        }}</span>
      </div>
      <ReleaseNotes :notes="r.notes" />
      <p class="muted">
        {{ r.package_kind === "file" ? "按文件更新" : "完整文件 / 整包" }} ·
        {{ r.file_name }} · 发布者：{{ r.published_by || "历史记录"
        }}<span v-if="r.base_version">
          · 适用旧版本 {{ r.base_version }} → {{ r.target_path }}</span
        >
      </p>
      <div class="row">
        <span>{{ r.platform }} / {{ r.arch }} / {{ r.hardware }}</span
        ><span class="muted">{{ size(r.size) }}</span
        ><a class="button primary" :href="r.download">下载文件 ↓</a>
      </div>
      <details>
        <summary>完整性校验</summary>
        <code>{{ r.sha256 }}</code>
        <p class="muted">SHA-256 用于检查传输完整性，不是发布者数字签名。</p>
      </details>
    </article>
    <button v-if="historyMore" :disabled="busy" @click="olderReleases">
      加载更早版本
    </button>
  </main>
  <main v-else-if="!user" class="login-page">
    <section class="login-story">
      <a class="brand" href="/"><img src="/icon.svg" alt="" />Firmware Hub</a>
      <div>
        <span class="eyebrow">BUILD. RELEASE. DELIVER.</span>
        <h1>固件与软件<br />更新中心。</h1>
        <p>
          面向设备与应用的轻量分发中心。<br />版本、发布与设备状态，在同一个地方。
        </p>
        <div class="story-grid">
          <span>01 / 账号署名</span><span>02 / 可靠传输</span
          ><span>03 / 按需采集</span>
        </div>
      </div>
      <small>清晰发布 · 轻量交付</small>
    </section>
    <section class="login-form"><AppearanceControls />
      <span class="eyebrow">WELCOME BACK</span>
      <h2>登录工作空间</h2>
      <p class="muted">使用管理员或项目成员账号继续。</p>
      <p><a href="/?public">无需登录，浏览公开版本 →</a></p>
      <form @submit.prevent="login">
        <label
          >用户名<input
            v-model="loginName"
            autocomplete="username"
            required /></label
        ><label
          >密码<input
            v-model="loginPassword"
            type="password"
            autocomplete="current-password"
            required
        /></label>
        <label
          >验证器校验码（仅启用后填写）<input
            v-model="loginOTP"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="6"
            placeholder="6 位校验码"
        /></label>
        <p v-if="error" class="alert error" role="alert">{{ error }}</p>
        <button class="primary" :disabled="busy">
          {{ busy ? "正在登录…" : "登录 →" }}
        </button>
      </form>
      <small class="muted"
        >首次账户由部署时 USER / PASSWORD 创建。<br />忘记密码可通过 admin-sync
        重置。</small
      >
    </section>
  </main>
  <div v-else class="shell" data-drawer-host>
    <button class="drawer-backdrop" type="button" data-drawer-close aria-label="关闭导航"></button>
    <aside id="hub-nav" class="sidebar" data-drawer aria-label="工作空间导航"><button class="drawer-close" type="button" data-drawer-close>关闭导航</button>
      <a class="brand" href="/"><img src="/icon.svg" alt="" />Firmware Hub</a>
      <div class="space-label">工作空间 <span>v0.3</span></div>
      <button
        class="new-project"
        @click="
          draft = freshProject();
          modal = 'project';
        "
      >
        ＋ 创建项目</button
      ><label class="project-select"
        >当前项目<select
          :value="selected ?? ''"
          @change="choose(Number(($event.target as HTMLSelectElement).value))"
        >
          <option v-if="!projects.length" value="">尚未创建项目</option>
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
          <span>{{ n[2] }}</span>{{ n[1]
          }}<i v-if="section === n[0]"></i></button
        ><button
          v-if="user.admin"
          :class="{ active: section === 'system' }"
          @click="navigate('system')"
        >
          <span>⊞</span>系统管理
        </button>
      </nav>
      <div class="sidebar-footer">
        <span class="avatar">{{ user.name[0]?.toUpperCase() }}</span>
        <div>
          <strong>{{ user.name }}</strong><small>{{
            user.admin ? "平台管理员" : "项目成员"
          }}</small>
        </div>
        <button title="退出登录" @click="logout">↪</button>
      </div>
    </aside>
    <div class="workspace">
      <header class="topbar"><button class="drawer-toggle" type="button" data-drawer-toggle aria-controls="hub-nav" aria-expanded="false" aria-label="展开导航">☰</button><AppearanceControls />
        <span class="muted"
          >工作空间 / {{ project?.name || "开始" }} /
          <b>{{
            navs.find((n) => n[0] === section)?.[1] || "系统管理"
          }}</b></span
        ><span class="top-status"><i></i> Hub 运行中</span>
      </header>
      <div class="content">
        <div class="page-heading">
          <div>
            <span class="eyebrow">{{
              section === "devices" ? "DEVICE VISIBILITY" : "FIRMWARE WORKSPACE"
            }}</span>
            <h1>{{ navs.find((n) => n[0] === section)?.[1] || "系统管理" }}</h1>
            <p>
              {{
                section === "devices"
                  ? "只采集你明确允许的状态，在线以最近一次心跳为准。"
                  : "管理版本发布、设备状态与通知。"
              }}
            </p>
          </div>
          <div class="row">
            <button :disabled="busy" @click="run(refreshAll)">↻ 刷新</button
            ><button
              v-if="section === 'releases' && project && write"
              class="primary"
              @click="modal = 'release'"
            >
              ＋ 上传版本</button
            ><button
              v-if="section === 'devices' && project && write"
              class="primary"
              @click="
                deviceToken = '';
                modal = 'device';
              "
            >
              ＋ 添加设备
            </button>
          </div>
        </div>
        <p v-if="error" class="alert error" role="alert">{{ error }}</p>
        <p v-if="message" class="alert success" role="status">{{ message }}</p>
        <template v-if="section === 'system' && user.admin"
          ><section class="card">
            <h2>状态采集总开关</h2>
            <p class="muted">
              关闭后，所有项目拒收设备心跳；已保存信息保留，可在各项目清除。
            </p>
            <label class="check"
              ><input
                v-model="globalTelemetry"
                type="checkbox"
              />允许项目按需接收设备状态</label
            ><button class="primary" :disabled="busy" @click="setGlobal">
              保存设置
            </button>
          </section>
          <section class="card">
            <h2>用户管理</h2>
            <form class="inline-form" @submit.prevent="createUser">
              <input
                v-model="newName"
                placeholder="用户名"
                required
                autocomplete="off"
              /><input
                v-model="newPassword"
                type="password"
                minlength="12"
                maxlength="72"
                placeholder="初始密码（至少 12 字节）"
                required
                autocomplete="new-password"
              /><button :disabled="busy">创建普通用户</button>
            </form>
            <div v-for="u in users" :key="u.id" class="list-row">
              <strong>{{ u.name }}</strong><span class="muted">{{
                u.admin ? "管理员" : "普通用户"
              }}</span>
            </div>
          </section>
          <ActivityPanel :request="api" :all="true"
        /></template>
        <template v-else-if="section === 'account'"
          ><ActivityPanel :request="api" />
          <section class="card">
            <h2>验证器二次验证</h2>
            <p class="muted">
              保护网页登录，不影响设备凭据和固件下载。支持常见 TOTP
              验证器；启用后请保存好验证器备份。
            </p>
            <span class="badge" :class="mfaEnabled ? 'green' : 'gray'">{{
              mfaEnabled ? "已启用" : "尚未启用"
            }}</span>
            <form
              @submit.prevent="
                configureMFA(
                  mfaEnabled ? 'disable' : mfaSecret ? 'confirm' : 'begin',
                )
              "
            >
              <label
                >当前登录密码<input
                  v-model="mfaPassword"
                  type="password"
                  required
                  autocomplete="current-password"
              /></label>
              <template v-if="mfaSecret"
                ><p class="hint">
                  请用验证器扫描二维码，或手动输入密钥。确认前不会启用。
                </p>
                <img
                  :src="mfaQR"
                  width="220"
                  height="220"
                  alt="验证器绑定二维码"
                /><code class="token">{{ mfaSecret }}</code></template
              >
              <label v-if="mfaEnabled || mfaSecret"
                >验证器当前校验码<input
                  v-model="mfaCode"
                  inputmode="numeric"
                  autocomplete="one-time-code"
                  pattern="[0-9]{6}"
                  maxlength="6"
                  required
              /></label>
              <button class="primary" :disabled="busy">
                {{
                  mfaEnabled
                    ? "验证并关闭"
                    : mfaSecret
                      ? "确认启用"
                      : "添加验证器"
                }}
              </button>
            </form>
            <p class="muted footnote">
              更改后撤销本账号全部会话并重新登录。验证器丢失可通过本机
              admin-sync 恢复管理员。
            </p>
          </section></template
        >
        <section v-else-if="!project" class="card empty">
          <div class="empty-icon">▣</div>
          <h2>从一个项目开始</h2>
          <p>
            创建项目后上传文件、填写版本与更新说明。默认按账号记录发布责任；数字签名可在项目设置中按需启用。
          </p>
          <button
            class="primary"
            @click="
              draft = freshProject();
              modal = 'project';
            "
          >
            创建第一个项目</button
          ><small class="muted">普通账号发布无需安装 fwtool。</small>
        </section>
        <template v-else>
          <div
            v-if="section === 'releases' || section === 'devices'"
            class="stats"
          >
            <article>
              <span>{{
                section === "devices" ? "已加载设备" : "已加载版本"
              }}</span><strong>{{
                section === "devices" ? devices.length : releases.length
              }}</strong
              ><small>{{ project.slug }}</small>
            </article>
            <article>
              <span>{{
                section === "devices" ? "加载设备在线" : "其中已发布"
              }}</span><strong>{{
                section === "devices"
                  ? online
                  : releases.filter((r) => r.published).length
              }}</strong
              ><small>{{
                section === "devices"
                  ? "不是持续连通性保证"
                  : "按账号记录发布责任"
              }}</small>
            </article>
            <article>
              <span>设备状态通道</span
              ><strong class="stat-text">{{
                project.telemetry && globalTelemetry ? "按需开启" : "已关闭"
              }}</strong
              ><small>客户端仍可独立关闭</small>
            </article>
          </div>
          <section v-if="section === 'releases'" class="card">
            <div class="card-heading">
              <h2>
                发布历史 <span class="count">{{ releases.length }}</span>
              </h2>
              <a
                v-if="project.public"
                class="text-link"
                :href="'/?project=' + project.slug"
                target="_blank"
                rel="noopener noreferrer"
                >查看公开发布页 ↗</a
              >
            </div>
            <div v-if="!releases.length" class="empty">
              <div class="empty-icon">↑</div>
              <h3>还没有发布版本</h3>
              <p>上传固件、软件整包或单个文件，填写版本说明后发布。</p>
            </div>
            <div class="release-toolbar">
              <label>搜索已加载版本<input v-model="releaseQuery" type="search" placeholder="版本、文件、平台或型号" /></label>
              <label>发布状态<select v-model="releaseStatus"><option value="all">全部状态</option><option value="published">已发布</option><option value="draft">草稿 / 已撤回</option></select></label>
              <p class="muted">显示 {{ filteredReleases.length }} / 已加载 {{ releases.length }} 条；更早记录请继续加载。</p>
            </div>
            <p v-if="releases.length && !filteredReleases.length" class="muted" role="status">已加载版本中没有匹配项。可调整筛选或加载更早版本。</p>
            <article v-for="r in filteredReleases" :key="r.id" class="release">
              <div class="release-summary">
                <div><small>版本</small><h3>{{ r.version }}</h3><span class="badge">{{ r.channel }}</span></div>
                <div><small>文件 / 平台</small><strong>{{ r.file_name }}</strong><span>{{ r.platform }} / {{ r.arch }} · {{ r.hardware }}</span></div>
                <div><small>大小 / 创建时间</small><span>{{ size(r.size) }}</span><time>{{ date(r.created) }}</time></div>
                <div><small>状态</small><span class="badge" :class="r.published ? 'green' : 'gray'">{{ r.published ? "已发布" : "草稿 / 已撤回" }}</span></div>
              </div>
              <details class="release-details">
                <summary>更新说明与发布信息</summary>
                <ReleaseNotes :notes="r.notes" />
                <p class="muted">{{ r.package_kind === "file" ? "按文件更新" : "完整文件 / 整包" }} · 发布序号 {{ r.sequence }}<span v-if="r.base_version"> · {{ r.base_version }} → {{ r.target_path }}</span></p>
                <p class="muted">上传：{{ r.uploaded_by }} · 发布：{{ r.published_by || "尚未发布" }}<span v-if="r.published_at"> · {{ date(r.published_at) }}</span></p>
              </details>
              <div class="row">
                <a class="button primary" :href="r.download">下载文件</a>
                <button v-if="publish" :disabled="busy" @click="toggleRelease(r)">{{ r.published ? "撤回版本" : "发布版本" }}</button>
                <details><summary>校验信息</summary><code>SHA-256: {{ r.sha256 }}</code><small>安全版本计数：{{ r.security }}</small></details>
              </div>
            </article>
            <button v-if="historyMore" :disabled="busy" @click="olderReleases">
              加载更早版本
            </button>
          </section>
          <section v-else-if="section === 'devices'" class="card">
            <div class="card-heading">
              <h2>客户端状态</h2>
              <button v-if="write" :disabled="busy" @click="clearStatus">
                清除已保存状态
              </button>
            </div>
            <p v-if="!project.telemetry || !globalTelemetry" class="hint">
              状态采集已关闭，设备仍可正常检查和下载固件。开启项目和全局开关后，还需客户端主动同意上报。
            </p>
            <p class="muted">
              心跳建议 {{ project.heartbeat }} 秒 · 超过
              {{ project.timeout }} 秒未收到心跳显示超时 · 每 15 秒刷新本页
            </p>
            <div v-if="!devices.length" class="empty">
              <h3>尚未登记设备</h3>
              <p>为每台设备分配独立凭据，避免设备之间互相覆盖状态。</p>
            </div>
            <div v-else class="table-wrap mobile-records">
              <table class="devices-table">
                <thead>
                  <tr>
                    <th>设备</th>
                    <th>在线状态</th>
                    <th>最后上报版本</th>
                    <th>升级状态</th>
                    <th>最近心跳</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="d in devices" :key="d.id">
                    <td data-label="设备">
                      <strong>{{ d.name }}</strong><small
                        >{{ d.platform || "—" }} / {{ d.arch || "—" }} /
                        {{ d.hardware || "—" }}</small
                      >
                    </td>
                    <td data-label="在线状态">
                      <span
                        class="badge"
                        :class="d.online_state === 'online' ? 'green' : 'gray'"
                        >{{ label(d.online_state) }}</span
                      >
                    </td>
                    <td data-label="最后上报版本">
                      <code>{{ d.version || "—" }}</code>
                    </td>
                    <td data-label="升级状态">{{ label(d.status) }}</td>
                    <td data-label="最近心跳">{{ date(d.last_seen) }}</td>
                    <td data-label="信息">
                      <button
                        v-if="write && !d.revoked"
                        class="danger-text"
                        :disabled="busy"
                        @click="revoke(d)"
                      >
                        撤销凭据
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p class="muted footnote">
              状态由客户端自报，不等于硬件可信证明。关闭客户端上报后，已有记录会在超时后显示为心跳超时。
            </p>
          </section>
          <section v-else-if="section === 'notifications'" class="card">
            <h2>发布即提醒</h2>
            <p class="muted">
              通知只提示检查新版本，不直接执行安装。渠道留空即关闭。凭据留空保留原值，填写单个
              - 清除。
            </p>
            <p v-if="!owner" class="hint">只有项目所有者可以管理通知凭据。</p>
            <form v-else @submit.prevent="saveNotice">
              <div class="form-grid">
                <label
                  >Webhook 地址<input
                    v-model="notice.webhook"
                    placeholder="https://example.com/webhook" /></label
                ><label
                  >Webhook 签名密钥<input
                    v-model="notice.webhook_secret"
                    type="password"
                    :placeholder="
                      notice.has_webhook_secret
                        ? '已保存，留空不变'
                        : '建议设置共享签名密钥'
                    "
                    autocomplete="new-password" /></label
                ><label
                  >Telegram Bot Token<input
                    v-model="notice.telegram_token"
                    type="password"
                    :placeholder="
                      notice.has_telegram_token
                        ? '已保存，留空不变'
                        : '留空不启用'
                    "
                    autocomplete="new-password" /></label
                ><label
                  >Telegram Chat ID<input
                    v-model="notice.telegram_chat"
                    placeholder="会话 ID" /></label
                ><label
                  >MQTT Broker<input
                    v-model="notice.mqtt_url"
                    placeholder="ssl://broker.example.com:8883" /></label
                ><label
                  >MQTT 主题<input
                    v-model="notice.mqtt_topic"
                    placeholder="firmware/project/available" /></label
                ><label
                  >MQTT 用户名<input
                    v-model="notice.mqtt_user"
                    autocomplete="off" /></label
                ><label
                  >MQTT 密码<input
                    v-model="notice.mqtt_password"
                    type="password"
                    :placeholder="
                      notice.has_mqtt_password ? '已保存，留空不变' : ''
                    "
                    autocomplete="new-password"
                /></label>
              </div>
              <label v-if="user.admin" class="check"
                ><input
                  v-model="notice.allow_private"
                  type="checkbox"
                />允许访问内网通知目标，以及内网 HTTP / TCP（仅可信目标）</label
              ><button class="primary" :disabled="busy">保存通知配置</button>
            </form>
          </section>
          <section v-else-if="section === 'logs'" class="card">
            <h2>项目操作记录</h2>
            <p class="muted">
              记录操作账号、版本发布/撤回与设置变更；最近 200
              条。账号署名不是数字签名。
            </p>
            <div v-for="a in audits" :key="a.id" class="list-row">
              <div>
                <strong>{{ a.name }}</strong>
                <p>{{ a.action }}</p>
                <small>{{ date(a.at) }}</small>
              </div>
            </div>
            <h2>通知投递记录</h2>
            <div v-if="!logs.length" class="empty">
              暂无投递记录。发布版本并配置渠道后会在这里显示。
            </div>
            <div v-for="l in logs" :key="l.id" class="list-row">
              <div>
                <strong>{{ l.kind }}</strong><small
                  >{{ date(l.created) }} · 已尝试 {{ l.attempts }} 次</small
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
            <h2>项目成员</h2>
            <p class="muted">
              维护者负责上传，审核者负责发布。所有者可同时完成两项操作。
            </p>
            <form v-if="owner" class="inline-form" @submit.prevent="saveMember">
              <input
                v-model="memberName"
                placeholder="已存在的用户名"
                required
              /><select v-model="memberRole">
                <option value="viewer">查看者</option>
                <option value="maintainer">维护者</option>
                <option value="reviewer">发布审核者</option>
                <option value="remove">移除成员</option></select
              ><button :disabled="busy">更新成员</button>
            </form>
            <div v-for="m in members" :key="m.id" class="list-row">
              <strong>{{ m.name }}</strong><span class="badge gray">{{
                label(m.role)
              }}</span>
            </div>
          </section>
          <section v-else-if="section === 'settings'" class="card">
            <h2>项目设置</h2>
            <form @submit.prevent="saveProject">
              <div class="form-grid">
                <label
                  >项目名称<input
                    v-model="draft.name"
                    :disabled="!owner"
                    required
                    maxlength="100" /></label
                ><label>项目标识<input :value="draft.slug" disabled /></label>
              </div>
              <label
                >项目介绍<textarea
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
                />公开发布页和已发布文件（设备状态始终不公开）</label
              >
              <hr />
              <h3>可选：设备状态通道</h3>
              <label class="check"
                ><input
                  v-model="draft.telemetry"
                  type="checkbox"
                  :disabled="!owner"
                />允许接收客户端当前版本、升级状态与心跳</label
              >
              <p class="muted">
                默认关闭；不会采集 IP、用户名或设备序列号。Hub
                总开关与客户端开关也必须允许。
              </p>
              <div class="form-grid">
                <label
                  >建议心跳间隔（秒）<input
                    v-model.number="draft.heartbeat"
                    type="number"
                    min="15"
                    max="86400"
                    :disabled="!owner" /></label
                ><label
                  >离线判定超时（秒）<input
                    v-model.number="draft.timeout"
                    type="number"
                    :min="draft.heartbeat * 2"
                    max="604800"
                    :disabled="!owner"
                /></label>
              </div>
              <hr />
              <h3>发布保护</h3>
              <p class="muted">
                当前：{{
                  draft.public_key
                    ? "高级数字签名模式"
                    : "账号发布模式，无需 fwtool"
                }}。账号模式信任服务器与账号权限，不能抵御服务器被入侵后替换更新。
              </p>
              <label
                >高级签名公钥<input
                  :value="draft.public_key"
                  readonly
                  class="mono"
              /></label>
              <p class="muted">
                保留高级模式以兼容已有客户端；默认新项目不需要公钥或签名工具。
              </p>
              <button
                v-if="owner && draft.public_key"
                type="button"
                @click="draft.public_key = ''"
              >
                改为账号发布模式
              </button>
              <label v-if="owner"
                >项目发布密码（可选，留空关闭；不修改则不要输入）<input
                  v-model="draft.publish_password"
                  type="password"
                  autocomplete="new-password"
                  :placeholder="
                    draft.has_publish_password
                      ? '已启用，未修改则保持'
                      : '未启用'
                  "
              /></label>
              <label
                v-if="
                  owner &&
                  (draft.publish_password !== undefined ||
                    draft.public_key !== project.public_key)
                "
                >验证当前账号密码<input
                  v-model="draft.account_password"
                  type="password"
                  autocomplete="current-password"
                  required
              /></label>
              <button v-if="owner" class="primary" :disabled="busy">
                保存项目设置
              </button>
            </form>
          </section>
        </template>
      </div>
      <footer class="site-footer">
        © 2026 Firmware Hub contributors · MIT License <button type="button" data-about-open>关于与许可</button> <span>账号署名 / 按需连接</span>
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
          ? '创建项目'
          : modal === 'release'
            ? '上传版本'
            : '添加设备'
      "
    >
      <div class="card-heading">
        <h2>
          {{
            modal === "project"
              ? "创建项目"
              : modal === "release"
                ? "上传版本"
                : "添加设备"
          }}
        </h2>
        <button
          :disabled="busy"
          title="关闭"
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
            >项目名称<input
              v-model="draft.name"
              required
              maxlength="100"
              placeholder="例如：设备控制器" /></label
          ><label
            >项目标识<input
              v-model="draft.slug"
              required
              pattern="[A-Za-z0-9][A-Za-z0-9._+\-]{0,63}"
              placeholder="device-controller"
          /></label>
        </div>
        <label
          >介绍<textarea v-model="draft.description" rows="2"></textarea>
        </label>
        <details>
          <summary>高级：独立数字签名（可选）</summary>
          <label
            >Ed25519 公钥（64 位十六进制）<input
              v-model="draft.public_key"
              pattern="[0-9a-fA-F]{64}"
              class="mono"
              placeholder="仅填写公钥，绝不上传私钥"
          /></label>
          <p class="hint">
            仅高级模式需要在本地运行
            <code>fwtool keygen --out publisher.key</code>。私钥留在发布者本地。
          </p>
        </details>
        <label
          >项目发布密码（可选，12–72 字节）<input
            v-model="draft.publish_password"
            type="password"
            autocomplete="new-password"
            placeholder="留空不启用"
        /></label>
        <label class="check"
          ><input
            v-model="draft.telemetry"
            type="checkbox"
          />启用可选的设备状态通道</label
        ><label class="check"
          ><input
            v-model="draft.public"
            type="checkbox"
          />公开已发布版本与下载</label
        ><button class="primary" :disabled="busy">创建项目</button>
      </form>
      <form v-else-if="modal === 'release'" @submit.prevent="upload">
        <p class="muted">
          上传后保存为草稿；发布时记录操作账号，不自动安装到客户端。
        </p>
        <div v-if="!project?.public_key" class="form-grid">
          <label
            >版本号<input
              v-model="uploadMeta.version"
              required
              pattern="[A-Za-z0-9][A-Za-z0-9._+\-]{0,63}"
              placeholder="1.2.0"
          /></label>
          <label
            >更新方式<select v-model="uploadMeta.package_kind">
              <option value="full">完整文件 / 软件整包</option>
              <option value="file">按文件更新（DLL / EXE 等）</option>
            </select></label
          >
          <template v-if="uploadMeta.package_kind === 'file'"
            ><label
              >适用旧版本<input
                v-model="uploadMeta.base_version"
                required
                placeholder="1.1.0" /></label
            ><label
              >软件目录内目标路径<input
                v-model="uploadMeta.target_path"
                required
                placeholder="MyApp.exe 或 plugins/module.dll" /></label
          ></template>
        </div>
        <p v-if="!project?.public_key" class="hint">
          整包可上传 windeployqt 生成目录的 ZIP（文件直接位于 ZIP
          根目录）；也可直接上传设备
          BIN。按文件更新只替换指定文件，不是二进制差分；仅向适用旧版本提供。
        </p>
        <details v-if="!project?.public_key">
          <summary>通道与兼容范围（可保持默认）</summary>
          <div class="form-grid">
            <label>通道<input v-model="uploadMeta.channel" required /></label
            ><label>平台<input v-model="uploadMeta.platform" required /></label
            ><label>架构<input v-model="uploadMeta.arch" required /></label
            ><label
              >硬件型号<input v-model="uploadMeta.hardware" required
            /></label>
          </div>
        </details>
        <label v-if="project?.public_key"
          >签名清单 manifest.json<input
            type="file"
            accept=".json"
            required
            @change="fileChange($event, 'manifest')" /></label
        ><label
          >对应固件 / 软件文件<input
            type="file"
            required
            @change="fileChange($event, 'artifact')" /></label
        ><label
          >版本说明（Markdown）<textarea
            v-model="uploadNotes"
            rows="6"
            maxlength="32768"
            placeholder="## 更新内容&#10;- 新增功能&#10;&#10;## 升级注意事项&#10;兼容型号与已知问题…"
          ></textarea>
        </label>
        <details class="editor-preview">
          <summary>预览版本说明</summary>
          <ReleaseNotes :notes="uploadNotes" />
        </details>
        <progress v-if="busy" :value="uploadProgress" max="100"></progress
        ><button class="primary" :disabled="busy">
          {{ busy ? `上传并校验 ${uploadProgress}%` : "保存草稿" }}
        </button>
      </form>
      <template v-else
        ><form v-if="!deviceToken" @submit.prevent="createDevice">
          <p class="muted">
            每台设备使用独立凭据。设备名称由你指定，不自动推断用户名或地址。
          </p>
          <label
            >设备名称<input
              v-model="deviceName"
              maxlength="100"
              placeholder="例如：测试终端 01"
              required /></label
          ><button class="primary" :disabled="busy">创建访问凭据</button>
        </form>
        <div v-else>
          <p class="hint">
            凭据仅显示这一次。可用于本项目更新检测、下载和可选心跳。
          </p>
          <code class="token">{{ deviceToken }}</code><button
            @click="copy(deviceToken)"
          >
            复制凭据
          </button>
          <p class="muted">不要写进公开仓库、截图或公开发布页。</p>
        </div></template
      >
    </section>
  </div>
</template>
