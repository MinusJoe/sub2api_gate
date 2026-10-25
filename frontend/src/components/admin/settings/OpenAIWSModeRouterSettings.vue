<template>
  <div class="card" data-testid="openai-ws-mode-router-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.openaiWSModeRouter.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.openaiWSModeRouter.description') }}</p>
    </div>
    <div class="space-y-5 p-6">
      <p v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <div v-else-if="loadFailed" class="space-y-3" data-testid="ws-mode-router-load-error">
        <p class="text-sm text-red-600">{{ t('admin.settings.openaiWSModeRouter.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary btn-sm" data-testid="ws-mode-router-retry" @click="load">{{ t('admin.settings.openaiWSModeRouter.retry') }}</button>
      </div>
      <template v-else>
        <div class="flex items-center justify-between gap-4">
          <div>
            <label class="font-medium text-gray-900 dark:text-white">{{ t('admin.settings.openaiWSModeRouter.enabled') }}</label>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.openaiWSModeRouter.hint') }}</p>
          </div>
          <Toggle v-model="enabled" :disabled="saving" :aria-label="t('admin.settings.openaiWSModeRouter.enabled')" data-testid="ws-mode-router-toggle" />
        </div>
        <p class="text-sm text-gray-500 dark:text-gray-400" data-testid="ws-mode-router-source">
          {{ t(settings?.source === 'web' ? 'admin.settings.openaiWSModeRouter.sourceWeb' : 'admin.settings.openaiWSModeRouter.sourceConfig') }}
        </p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.openaiWSModeRouter.note') }}</p>
        <div class="flex flex-wrap justify-end gap-3 border-t border-gray-100 pt-4 dark:border-dark-700">
          <button type="button" class="btn btn-secondary btn-sm" :disabled="saving || settings?.source === 'config'" data-testid="ws-mode-router-inherit" @click="save(null)">{{ t('admin.settings.openaiWSModeRouter.inherit') }}</button>
          <button type="button" class="btn btn-primary btn-sm" :disabled="saving" data-testid="ws-mode-router-save" @click="save(enabled)">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores'
import { getOpenAIWSModeRouterSettings, updateOpenAIWSModeRouterSettings, type OpenAIWSModeRouterSettings } from '@/api/admin/settings'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const loadFailed = ref(false)
const enabled = ref(false)
const settings = ref<OpenAIWSModeRouterSettings | null>(null)

function apply(value: OpenAIWSModeRouterSettings) {
  settings.value = value
  enabled.value = value.enabled
}

async function load() {
  loading.value = true
  loadFailed.value = false
  try { apply(await getOpenAIWSModeRouterSettings()) }
  catch { loadFailed.value = true }
  finally { loading.value = false }
}

async function save(value: boolean | null) {
  if (saving.value || loadFailed.value || !settings.value) return
  saving.value = true
  try {
    apply(await updateOpenAIWSModeRouterSettings(value))
    appStore.showSuccess(t('admin.settings.openaiWSModeRouter.saved'))
  } catch (error) {
    appStore.showError((error as { message?: string })?.message ?? t('admin.settings.openaiWSModeRouter.saveFailed'))
  } finally { saving.value = false }
}

onMounted(load)
</script>
