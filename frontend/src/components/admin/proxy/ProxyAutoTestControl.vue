<template>
  <div class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-600" data-testid="proxy-auto-test">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
      <label class="flex shrink-0 cursor-pointer items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-200">
        <input
          type="checkbox"
          class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
          :checked="enabled"
          data-testid="proxy-auto-test-toggle"
          @change="emit('update:enabled', ($event.target as HTMLInputElement).checked)"
        />
        {{ t('admin.proxies.autoTest.title') }}
      </label>
      <div class="flex flex-wrap items-center gap-2">
        <div class="w-32">
          <Select
            :model-value="selection"
            :options="intervalOptions"
            :searchable="false"
            :aria-label="t('admin.proxies.autoTest.interval')"
            @update:model-value="selectInterval"
          />
        </div>
        <form v-if="selection === 'custom'" class="flex items-center gap-2" @submit.prevent="applyCustomInterval">
          <input
            v-model="customSeconds"
            type="number"
            min="1"
            step="1"
            required
            class="input w-24 tabular-nums"
            :aria-label="t('admin.proxies.autoTest.customSeconds')"
            :aria-invalid="invalid || undefined"
            :aria-describedby="invalid ? 'proxy-auto-test-interval-error' : undefined"
            data-testid="proxy-auto-test-custom"
            @input="invalid = false"
          />
          <span class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.proxies.autoTest.secondsUnit') }}</span>
          <button type="submit" class="btn btn-secondary" data-testid="proxy-auto-test-apply">
            {{ t('admin.proxies.autoTest.apply') }}
          </button>
        </form>
      </div>
      <span class="text-xs text-gray-500 dark:text-gray-400">
        {{ selectedCount > 0
          ? t('admin.proxies.autoTest.selectedScope', { count: selectedCount })
          : t('admin.proxies.autoTest.filteredScope') }}
      </span>
      <span
        v-if="enabled"
        class="inline-flex items-center gap-1.5 text-xs tabular-nums"
        :class="failed ? 'text-red-600 dark:text-red-400' : 'text-primary-700 dark:text-primary-300'"
        data-testid="proxy-auto-test-status"
      >
        <Icon name="clock" size="sm" />
        {{ statusText }}
      </span>
    </div>
    <p v-if="invalid" id="proxy-auto-test-interval-error" role="alert" class="mt-2 text-xs text-red-600 dark:text-red-400">
      {{ t('admin.proxies.autoTest.invalidInterval') }}
    </p>
    <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.autoTest.hint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { isValidProxyTestInterval, PROXY_TEST_INTERVALS } from '@/composables/useProxyAutoTest'

const props = defineProps<{
  enabled: boolean
  intervalSeconds: number
  countdown: number
  running: boolean
  paused: boolean
  failed: boolean
  selectedCount: number
}>()
const emit = defineEmits<{
  'update:enabled': [value: boolean]
  'update:interval': [value: number]
}>()
const { t } = useI18n()
const selection = ref<number | 'custom'>(30)
const customSeconds = ref<number | string>(props.intervalSeconds)
const invalid = ref(false)

watch(() => props.intervalSeconds, value => {
  selection.value = PROXY_TEST_INTERVALS.some(interval => interval === value) ? value : 'custom'
  customSeconds.value = value
}, { immediate: true })

const intervalOptions = computed(() => [
  ...PROXY_TEST_INTERVALS.map(value => ({ value, label: `${value}s` })),
  { value: 'custom', label: t('admin.proxies.autoTest.custom') }
])
const statusText = computed(() => {
  if (props.paused) return t('admin.proxies.autoTest.paused')
  if (props.running) return t('admin.proxies.autoTest.running')
  if (props.failed) return t('admin.proxies.autoTest.retry', { seconds: props.countdown })
  return t('admin.proxies.autoTest.countdown', { seconds: props.countdown })
})

function selectInterval(value: string | number | boolean | null) {
  invalid.value = false
  if (value === 'custom') {
    selection.value = 'custom'
    return
  }
  if (typeof value === 'number' && isValidProxyTestInterval(value)) {
    selection.value = value
    emit('update:interval', value)
  }
}
function applyCustomInterval() {
  const seconds = Number(customSeconds.value)
  invalid.value = !isValidProxyTestInterval(seconds)
  if (!invalid.value) emit('update:interval', seconds)
}
</script>
