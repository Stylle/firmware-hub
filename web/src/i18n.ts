import { ref } from 'vue';
const current = ref('zh');
const get = () => (window as any).AppI18n;
current.value = get()?.lang || 'zh';
window.addEventListener('ui-language-change', () => { current.value = get()?.lang || 'zh'; });
/** Translates interface literals only; never use for user-supplied content. */
export function t(value: string): string { void current.value; return get()?.t(value) ?? value; }

