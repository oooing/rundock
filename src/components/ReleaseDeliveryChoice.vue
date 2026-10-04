<script setup lang="ts">
import { tr } from '@/i18n'
import type { ReleaseTarget } from '@/types'
defineProps<{ targets: Array<{ target: ReleaseTarget; choice: { publish: boolean } }>; disabled?: boolean }>()
const emit = defineEmits<{ change: [targetId: string, publish: boolean] }>()
</script>

<template>
  <fieldset v-if="targets.length" class="delivery-choice" :disabled="disabled">
    <legend>{{ tr('产物发布到哪里') }}</legend>
    <div v-for="{ target, choice } in targets" :key="target.id" class="destination">
      <label :for="`delivery-${target.id}`">{{ target.name }}</label>
      <select :id="`delivery-${target.id}`" :value="choice.publish ? 'github' : 'local'"
        @change="emit('change', target.id, ($event.target as HTMLSelectElement).value === 'github')">
        <option value="local">{{ tr('仅保存在本机') }}</option>
        <option value="github" :disabled="!target.delivery">GitHub Release</option>
      </select>
      <small v-if="!target.delivery">{{ tr('在发布配置中设置仓库、账号、必需产物和验证命令后，可启用 GitHub 交付。') }}</small>
      <small v-else-if="choice.publish">{{ target.delivery.repository }} · {{ target.delivery.account }} · {{ tr('文件齐全并核验后正式发布') }}</small>
    </div>
    <p v-if="targets.some(item => item.choice.publish)" role="note">{{ tr('将同步本次代码和 Tag；上传失败后可使用保存的安装包继续。') }}</p>
  </fieldset>
</template>

<style scoped>
.delivery-choice { border: 1px solid var(--border); border-radius: 8px; padding: 12px; margin: 8px 0; }
legend { padding: 0 5px; font-weight: 600; }
.destination { display: grid; grid-template-columns: minmax(100px, 1fr) minmax(160px, 1fr); gap: 8px; align-items: center; margin: 8px 0; }
select { min-height: 36px; border: 1px solid var(--border); border-radius: 6px; background: var(--bg); color: var(--text); padding: 6px; }
small, p { grid-column: 1 / -1; color: var(--text-dim); font-size: 12px; line-height: 1.6; margin: 0; }
@media (max-width: 460px) { .destination { grid-template-columns: 1fr; } }
</style>
