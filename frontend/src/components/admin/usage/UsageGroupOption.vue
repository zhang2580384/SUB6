<template>
  <span :title="label">
    <GroupBadge
      v-if="group?.platform"
      :name="label"
      :platform="group.platform"
      :subscription-type="group.subscription_type"
      :rate-multiplier="group.rate_multiplier"
      :peak-rate-enabled="group.peak_rate_enabled"
      :peak-start="group.peak_start"
      :peak-end="group.peak_end"
      :peak-rate-multiplier="group.peak_rate_multiplier"
      :truncate="false"
      class="usage-group-badge max-w-full text-left"
    />
    <span v-else class="whitespace-normal [overflow-wrap:anywhere]">{{ label }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Group } from '@/types'
import GroupBadge from '@/components/common/GroupBadge.vue'

const props = defineProps<{ option?: Record<string, unknown> | null }>()
const { t } = useI18n()
const label = computed(() => String(props.option?.label ?? t('common.selectOption')))
// Observer options intentionally contain labels only; use their existing scope
// without requesting the administrator's group configuration.
const group = computed(() => props.option?.group as Partial<Group> | undefined)
</script>

<style scoped>
.usage-group-badge {
  display: inline-grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: start;
}

.usage-group-badge :deep([title]) {
  grid-column: 2 / -1;
}
</style>
