<template>
  <fieldset class="space-y-2 border-t border-gray-200 pt-4 dark:border-dark-600">
    <legend class="input-label">{{ t('admin.accounts.openai.ufPromoTitle') }}</legend>
    <p class="text-xs text-gray-500 dark:text-gray-400">
      {{ t(expired ? 'admin.accounts.openai.ufPromoEnded' : 'admin.accounts.openai.ufPromoHint') }}
    </p>
    <label v-for="entry in models" :key="entry.key" class="flex items-center gap-2 text-sm">
      <input
        type="checkbox"
        :data-testid="`uf-promo-${entry.key}`"
        :checked="!expired && modelValue[entry.key]"
        :disabled="expired"
        @change="change(entry.key, $event)"
      />
      {{ entry.name }}
    </label>
  </fieldset>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UFPromoModel, UFPromotions } from './gpt62UFPromotions'

const { t } = useI18n()
const props = defineProps<{ modelValue: UFPromotions }>()
const emit = defineEmits<{ 'update:modelValue': [UFPromotions] }>()
const models = [{ key: 'sol', name: 'GPT-6.2 Sol' }, { key: 'astra', name: 'GPT-6.2 Astra' }] as const
const deadline = Date.parse('2027-03-01T08:00:00Z')
const expired = ref(Date.now() >= deadline)
let timer: ReturnType<typeof setTimeout> | undefined
function refreshExpiry() {
  const remaining = deadline - Date.now()
  expired.value = remaining <= 0
  if (remaining > 0) timer = setTimeout(refreshExpiry, Math.min(remaining, 2147483647))
}
onMounted(refreshExpiry)
onUnmounted(() => clearTimeout(timer))

function change(model: UFPromoModel, event: Event) {
  if (Date.now() >= deadline) return
  emit('update:modelValue', { ...props.modelValue, [model]: (event.target as HTMLInputElement).checked })
}
</script>
