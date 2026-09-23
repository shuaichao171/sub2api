<template>
  <div class="space-y-2">
    <label class="input-label">{{ t('admin.accounts.ticketPolicy') }}</label>
    <Select v-model="policy" :options="options" />
    <p class="input-hint" role="status">{{ effectiveText }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import Select from '@/components/common/Select.vue'

const policy = defineModel<'inherit' | 'allow' | 'deny'>({ required: true })
const { t } = useI18n()
const globalPolicy = ref<{ enabled: boolean; allow: boolean } | null>(null)
const failed = ref(false)
const options = computed(() => [
  { value: 'inherit', label: t('admin.accounts.ticketInherit') },
  { value: 'allow', label: t('admin.accounts.ticketAllow') },
  { value: 'deny', label: t('admin.accounts.ticketDeny') }
])
const effectiveText = computed(() => {
  if (failed.value) return t('admin.accounts.ticketPolicyLoadError')
  if (!globalPolicy.value) return t('common.loading')
  if (!globalPolicy.value.enabled) return t('admin.accounts.ticketDisabled')
  const allow = policy.value === 'inherit' ? globalPolicy.value.allow : policy.value === 'allow'
  return t('admin.accounts.ticketEffective', { policy: t(allow ? 'admin.accounts.ticketAllow' : 'admin.accounts.ticketDeny') })
})
onMounted(async () => {
  try {
    const settings = await adminAPI.settings.getSettings()
    globalPolicy.value = { enabled: settings.openai_codex_ticket_enabled, allow: settings.openai_codex_ticket_allow_without_ticket }
  } catch {
    failed.value = true
  }
})
</script>
