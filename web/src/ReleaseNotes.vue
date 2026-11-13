<script setup lang="ts">
import { computed } from "vue";
import { marked } from "marked";
import DOMPurify from "dompurify";
const props = defineProps<{ notes: string }>();
// No remote images, inline styles, SVG, embedded controls or scripts.
const html = computed(() =>
  DOMPurify.sanitize(
    marked.parse(props.notes || "暂无版本说明", { async: false }),
    {
      ALLOWED_TAGS: [
        "p",
        "br",
        "strong",
        "em",
        "del",
        "ul",
        "ol",
        "li",
        "h1",
        "h2",
        "h3",
        "h4",
        "blockquote",
        "pre",
        "code",
        "table",
        "thead",
        "tbody",
        "tr",
        "th",
        "td",
        "hr",
        "a",
      ],
      ALLOWED_ATTR: ["href", "title"],
      ALLOW_DATA_ATTR: false,
      ALLOWED_URI_REGEXP: /^(?:https?:|mailto:|#|\/(?!\/))/i,
    },
  ),
);
</script>
<template><div class="release-notes" v-html="html"></div></template>
