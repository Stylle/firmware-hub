<script setup lang="ts">
import { ref, onMounted } from "vue";
const props = defineProps<{
  request: (path: string, options?: RequestInit) => Promise<any>;
  all?: boolean;
}>();
type Session = {
  id: string;
  name: string;
  peer: string;
  agent: string;
  created: number;
  last_seen: number;
  expires: number;
  current: boolean;
  active: boolean;
};
type Entry = {
  id: number;
  name: string;
  success: boolean;
  reason: string;
  peer: string;
  agent: string;
  at: number;
};
const sessions = ref<Session[]>([]),
  history = ref<Entry[]>([]),
  busy = ref(false),
  error = ref("");
const date = (v: number) =>
  Number.isFinite(v) && v > 0 ? new Date(v * 1000).toLocaleString() : "暂无记录";
async function load() {
  busy.value = true;
  error.value = "";
  try {
    const data = await props.request("/activity" + (props.all ? "?all=1" : ""));
    sessions.value = data.sessions;
    history.value = data.history;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function revoke(s: Session) {
  if (!confirm(`撤销 ${s.name} 的${s.current ? "当前" : "这个"}登录会话？`))
    return;
  busy.value = true;
  error.value = "";
  try {
    await props.request("/sessions/" + encodeURIComponent(s.id), {
      method: "DELETE",
    });
    if (s.current) {
      location.reload();
      return;
    }
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
onMounted(load);
</script>
<template>
  <section class="card activity-panel">
    <div class="card-heading">
      <h2>{{ all ? "账号登录与会话" : "我的登录与会话" }}</h2>
      <button :disabled="busy" @click="load">刷新记录</button>
    </div>
    <p class="muted">
      “最近活跃”指 15
      分钟内有网页请求，不代表持续在线。来源是服务器直连地址；反向代理后可能显示代理地址。登录记录保留
      90 天、最多 5000 条，这里显示最近 100 条。
    </p>
    <p v-if="error" class="alert error" role="alert">{{ error }}</p>
    <h3>未过期的登录会话</h3>
    <p v-if="!sessions.length" class="muted">
      没有可显示的会话记录；旧会话将在下次登录后登记。
    </p>
    <article v-for="s in sessions" :key="s.id" class="list-row">
      <div>
        <strong
          >{{ s.name }}
          <span class="badge" :class="s.active ? 'green' : 'gray'">{{
            s.current ? "当前会话" : s.active ? "最近活跃" : "未活跃"
          }}</span></strong
        ><small>{{ s.peer }} · 最近访问 {{ date(s.last_seen) }}</small
        ><small>{{ s.agent || "未知客户端" }}</small
        ><small>登录 {{ date(s.created) }} · 到期 {{ date(s.expires) }}</small>
      </div>
      <button :disabled="busy" @click="revoke(s)">退出此会话</button>
    </article>
    <details>
      <summary>登录历史（{{ history.length }}）</summary>
      <article v-for="h in history" :key="h.id" class="list-row">
        <div>
          <strong>{{ h.name || "未知账号" }}</strong><small
            >{{ date(h.at) }} · {{ h.peer }}</small
          ><small>{{ h.agent || "未知客户端" }}</small>
        </div>
        <span class="badge" :class="h.success ? 'green' : 'gray'">{{
          h.success
            ? "成功"
            : h.reason === "invalid_otp"
              ? "验证码失败"
              : "登录失败"
        }}</span>
      </article>
    </details>
  </section>
</template>
<style scoped>
.activity-panel small {
  display: block;
  overflow-wrap: anywhere;
  max-width: 65ch;
}
.activity-panel .list-row {
  align-items: flex-start;
  gap: 12px;
  flex-wrap: wrap;
}
.activity-panel article > div {
  min-width: 0;
  flex: 1 1 230px;
}
.activity-panel details {
  margin-top: 20px;
}
</style>
