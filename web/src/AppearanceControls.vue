<script setup lang="ts">
import { onMounted } from 'vue'
onMounted(() => window.dispatchEvent(new Event('appearance-ready')))
</script>
<template>
 <div class="appearance-controls">
  <div class="theme-switch" aria-label="Theme / 语言">
   <button type="button" data-theme-choice="light" aria-label="亮色" title="亮色">☀️</button>
   <button type="button" data-theme-choice="dark" aria-label="暗色" title="暗色">🌙</button>
   <button type="button" data-lang-toggle aria-label="切换语言" title="中文 / English">中</button>
  </div>
  <button type="button" data-appearance-open aria-label="外观设置" title="外观设置">⚙</button>
  <button type="button" data-about-open aria-label="关于" title="关于">ⓘ</button>
 </div>
</template>
