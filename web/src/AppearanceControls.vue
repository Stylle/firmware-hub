<script setup lang="ts">
import { onMounted } from 'vue'
withDefaults(defineProps<{showAbout?:boolean}>(), {showAbout:true})
onMounted(() => window.dispatchEvent(new Event('appearance-ready')))
</script>
<template>
 <div class="appearance-controls">
  <div class="theme-switch" aria-label="Theme / 语言">
   <button type="button" data-theme-choice="light" aria-label="亮色" title="亮色"><svg class="ui-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M12 2v2 M12 20v2 M2 12h2 M20 12h2 M5 5l1.5 1.5 M17.5 17.5L19 19 M5 19l1.5-1.5 M17.5 6.5L19 5 M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0" /></svg></button>
   <button type="button" data-theme-choice="dark" aria-label="暗色" title="暗色"><svg class="ui-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M20 14A8 8 0 0 1 10 4a8 8 0 1 0 10 10" /></svg></button>
   <button type="button" data-theme-choice="system" aria-label="跟随系统" title="跟随系统"><svg class="ui-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M3 4h18v12H3z M8 20h8 M12 16v4" /></svg></button>
      <button type="button" data-lang-toggle aria-label="切换语言" title="中文 / English">中</button>
  </div>
  <button v-if="showAbout" type="button" data-about-open aria-label="关于" title="关于"><svg class="ui-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M12 8h.01 M12 11v6 M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0" /></svg></button>
 </div>
</template>
