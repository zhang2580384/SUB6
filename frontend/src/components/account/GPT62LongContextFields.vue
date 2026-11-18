<template>
  <fieldset class="space-y-2 border-t border-gray-200 pt-4 dark:border-dark-600">
    <legend class="sr-only">{{ t('admin.accounts.openai.gpt62LongContextTitle') }}</legend>
    <div class="flex items-center justify-between gap-4">
      <label :for="toggleId" class="input-label mb-0">{{ t('admin.accounts.openai.gpt62LongContextTitle') }}</label>
      <Toggle
        :id="toggleId"
        data-testid="gpt62-long-context-override"
        :model-value="selection.enabled"
        @update:model-value="selection = { ...selection, enabled: $event }"
      />
    </div>
    <template v-if="selection.enabled">
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openai.gpt62LongContextHint') }}</p>
      <label v-for="model in gpt62LongContextModels" :key="model.id" class="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          :data-testid="`gpt62-long-context-${model.id}`"
          :checked="selection[model.id]"
          @change="selection = { ...selection, [model.id]: ($event.target as HTMLInputElement).checked }"
        />
        {{ model.name }}
      </label>
    </template>
  </fieldset>
</template>

<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { gpt62LongContextModels, type GPT62LongContext } from './gpt62LongContext'

const { t } = useI18n()
const toggleId = useId()
const selection = defineModel<GPT62LongContext>({ required: true })
</script>
