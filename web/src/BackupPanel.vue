<script setup lang="ts">
import { t } from './i18n'
import { ref, onBeforeUnmount } from 'vue'
import { waitForRestoredSession } from './restore-wait.mjs'
const lifecycle = new AbortController()
onBeforeUnmount(() => lifecycle.abort())
const props = defineProps({ base: { type: String, required: true }, csrf: { type: String, default: '' } })
function chooseFile(event: Event) { file.value = (event.target as HTMLInputElement).files?.[0] || null; confirm.value = false }
const fileInput = ref<HTMLInputElement | null>(null)
const busy = ref(false), file = ref<File | null>(null), confirm = ref(false), message = ref(''), failed = ref(false)
async function download() {
 busy.value = true; message.value = ''; failed.value = false
 try {
  const link = document.createElement('a'); link.href = props.base + '/backup'; link.download = ''
  link.click()
  message.value = '备份请求已提交，请在浏览器下载列表查看进度与结果。'
 } catch (error) { failed.value = true; message.value = error instanceof Error ? error.message : String(error) } finally { busy.value = false }
}
async function restore() {
 if (!file.value || !confirm.value || busy.value) return
 busy.value = true; message.value = ''; failed.value = false
 try {
  const csrf = props.csrf || decodeURIComponent(document.cookie.split('; ').find(x => x.startsWith('csrf='))?.split('=').slice(1).join('=') || '')
  const response = await fetch(props.base + '/restore', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/zip', 'X-Confirm-Restore': 'replace', 'X-CSRF-Token': csrf }, body: file.value })
  if (!response.ok) { const data = await response.json().catch(() => ({})); throw Error(data.message || data.error || data.detail || '恢复失败，当前数据未替换。') }
  message.value = '备份已校验，服务正在重新加载。请使用备份中的账号重新登录。'
  if (await waitForRestoredSession('/api/v1/session', lifecycle.signal)) location.reload()
 } catch (error) { failed.value = true; message.value = error instanceof Error ? error.message : String(error); busy.value = false }
}
</script>

<template>
 <section class="card panel backup-panel">
  <h2>{{ t("系统备份与恢复") }}</h2>
  <p>{{ t("备份包含账号、系统配置、业务数据、附件及应用密钥，可在同项目的全新部署中导入。恢复后需重新登录。") }}</p>
  <p class="muted">{{ t("备份包包含敏感信息，请妥善保存。外部服务地址和账号仍需可用；部署端口、域名、挂载目录等由新环境配置。") }}</p>
  <button class="primary" :disabled="busy" @click="download">{{ t(busy ? '处理中…' : '下载完整备份包') }}</button>
  <hr />
  <h3>{{ t('从备份恢复') }}</h3>
  <div class="backup-actions">
   <input ref="fileInput" class="backup-file-input" type="file" accept=".zip,application/zip" :disabled="busy" @change="chooseFile" />
   <button type="button" class="backup-pick" :disabled="busy" @click="fileInput?.click()"><span aria-hidden="true">＋</span>{{ t('选择备份包') }}</button>
   <span class="backup-file-name" :title="file?.name || ''">{{ file ? file.name + ' · ' + (file.size / 1048576).toFixed(2) + ' MB' : t('尚未选择备份包') }}</span>
  </div>
  <label class="backup-confirm"><input v-model="confirm" type="checkbox" :disabled="busy" />{{ t("我已备份当前数据，确认用备份包替换。原数据将保留在服务器回退目录中。") }}</label>
  <button class="backup-restore" :disabled="busy || !file || !confirm" @click="restore">{{ t("校验并导入备份") }}</button>
  <p v-if="message" :class="{ error: failed }" role="status">{{ message }}</p>
 </section>
</template>

<style scoped>
.backup-panel{display:grid;gap:16px;min-width:0}.backup-panel p{margin:0;line-height:1.7}.backup-panel hr{width:100%;border:0;border-top:1px solid var(--border)}.backup-panel button{justify-self:start}.backup-panel input[type=file]{max-width:100%;width:100%}.backup-confirm{display:flex;flex-direction:row;align-items:flex-start;gap:10px;line-height:1.6}.backup-confirm input{flex:0 0 18px;width:18px;margin-top:3px}
.backup-actions{display:flex;align-items:center;gap:12px;flex-wrap:wrap;min-width:0}.backup-panel h3{margin:0;font-size:17px}.backup-file-input{display:none!important}.backup-panel .backup-pick,.backup-panel .backup-restore{display:inline-flex;align-items:center;justify-content:center;gap:8px;min-height:42px;padding:10px 16px;border-radius:8px;font:inherit;font-size:14px;cursor:pointer}.backup-panel .backup-pick{border:1px solid var(--border);background:var(--surface-solid);color:var(--text)}.backup-panel .backup-pick:hover:not(:disabled){background:var(--accent-soft)}.backup-panel .backup-restore{border:1px solid var(--err);background:transparent;color:var(--err)}.backup-panel button:disabled{opacity:.5;cursor:not-allowed}.backup-file-name{flex:1;min-width:0;font-size:12px;color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.backup-panel .backup-restore:hover:not(:disabled){background:var(--surface-hover)}@media(max-width:600px){.backup-file-name{flex-basis:100%}.backup-panel{gap:14px}.backup-panel>button{width:100%;justify-self:stretch}}
</style>
