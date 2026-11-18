<template>
  <ChannelStatusV3View v-if="mode === 'v3'" />
  <ChannelStatusV1View v-else-if="mode === 'v1'" />
  <ChannelStatusV2View v-else :key="mode" :statistics-version="mode" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { isChannelMonitorV1Mode, isChannelMonitorV3Mode, isChannelMonitorV4Mode } from '@/utils/featureFlags'
import ChannelStatusV1View from './ChannelStatusV1View.vue'
import ChannelStatusV2View from './ChannelStatusV2View.vue'
import ChannelStatusV3View from './ChannelStatusV3View.vue'

const mode = computed(() => (isChannelMonitorV3Mode() ? 'v3' : isChannelMonitorV1Mode() ? 'v1' : isChannelMonitorV4Mode() ? 'v4' : 'v2'))
</script>
