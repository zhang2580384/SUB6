<template>
  <component
    :is="passive ? 'span' : 'button'"
    v-if="visible"
    :type="passive ? undefined : 'button'"
    data-testid="new-feature-badge"
    :role="passive ? 'img' : undefined"
    :aria-label="t(passive ? 'common.newFeature' : 'common.dismissNewFeature')"
    :title="t(passive ? 'common.newFeature' : 'common.dismissNewFeature')"
    :class="[
      'inline-flex shrink-0 items-center justify-center rounded-full border-0 p-0 align-middle',
      passive || compact ? 'h-2 w-2' : 'h-4 w-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-red-500',
    ]"
    @click="dismiss"
  >
    <span aria-hidden="true" class="h-1.5 w-1.5 rounded-full bg-red-600 dark:bg-red-400" />
  </component>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useNewFeatureNoticeStore } from '@/stores/newFeatureNotices'

const props = withDefaults(defineProps<{
  noticeId: string
  userId: number | null | undefined
  compact?: boolean
  passive?: boolean
  noticeVersion?: string
}>(), {
  compact: false,
  passive: false,
  noticeVersion: '1',
})

const { t } = useI18n()
const notices = useNewFeatureNoticeStore()
watch(() => props.userId, (userId) => notices.loadUser(userId), { immediate: true })
const visible = computed(() => props.userId != null && !notices.isDismissed(props.userId, props.noticeId, props.noticeVersion))

function dismiss(event: MouseEvent): void {
  if (props.passive) return
  event.stopPropagation()
  event.preventDefault()
  notices.dismiss(props.userId, props.noticeId, props.noticeVersion)
}
</script>
