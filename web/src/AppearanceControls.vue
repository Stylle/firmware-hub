<script setup lang="ts">
import { onMounted } from 'vue'
onMounted(() => window.dispatchEvent(new Event('appearance-ready')))
</script>
<template>
 <div class="appearance-controls" aria-label="外观与关于">
  <label><span>主题</span><select data-theme-select aria-label="界面主题">
   <option value="light">浅灰</option><option value="dark">暗黑</option><option value="green">墨绿</option><option value="system">跟随系统</option>
  </select></label>
  <button type="button" data-about-open>关于</button>
 </div>
</template>

