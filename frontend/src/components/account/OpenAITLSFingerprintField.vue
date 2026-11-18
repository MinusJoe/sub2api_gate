<template>
  <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
    <label :for="inputId" class="input-label">{{ t('admin.accounts.quotaControl.tlsFingerprint.label') }}</label>
    <p :id="`${inputId}-hint`" class="mb-2 text-xs text-gray-500 dark:text-gray-400">
      {{ t('admin.accounts.quotaControl.tlsFingerprint.openAIModeHint') }}
    </p>
    <select
      :id="inputId"
      v-model="mode"
      :aria-describedby="`${inputId}-hint`"
      class="input"
      data-testid="openai-tls-fingerprint-mode"
    >
      <option value="inherit">{{ t('admin.accounts.quotaControl.tlsFingerprint.inheritGlobal') }}</option>
      <option value="on">{{ t('admin.accounts.quotaControl.tlsFingerprint.enable') }}</option>
      <option value="off">{{ t('admin.accounts.quotaControl.tlsFingerprint.disable') }}</option>
    </select>
    <div v-if="enabledOverride !== false" class="mt-3">
      <label :for="`${inputId}-profile`" class="input-label">
        {{ t('admin.accounts.quotaControl.tlsFingerprint.profileLabel') }}
      </label>
      <select :id="`${inputId}-profile`" v-model="profileId" class="input">
        <option :value="null">{{ t('admin.accounts.quotaControl.tlsFingerprint.defaultProfile') }}</option>
        <option v-if="profiles.length > 0" :value="-1">{{ t('admin.accounts.quotaControl.tlsFingerprint.randomProfile') }}</option>
        <option v-for="profile in profiles" :key="profile.id" :value="profile.id">{{ profile.name }}</option>
      </select>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

defineProps<{ profiles: { id: number; name: string }[] }>()

const enabledOverride = defineModel<boolean | null>({ required: true })
const profileId = defineModel<number | null>('profileId', { required: true })
const mode = computed<'inherit' | 'on' | 'off'>({
  get: () => enabledOverride.value === null ? 'inherit' : enabledOverride.value ? 'on' : 'off',
  set: (value) => { enabledOverride.value = value === 'inherit' ? null : value === 'on' },
})
const inputId = useId()
const { t } = useI18n()
</script>
