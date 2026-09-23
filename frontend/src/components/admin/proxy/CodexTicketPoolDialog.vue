<template>
  <BaseDialog :show="show" :title="t('admin.accounts.codexTicket.poolTitle')" width="normal" @close="$emit('close')">
    <div class="space-y-5">
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.poolHint') }}</p>
      <div v-if="loading" class="py-8 text-center text-sm text-gray-500">{{ t('admin.accounts.codexTicket.loading') }}</div>
      <template v-else>
        <div class="space-y-2">
          <label class="flex cursor-pointer items-center gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600"><input v-model="mode" type="radio" value="all" class="text-primary-600" />{{ t('admin.accounts.codexTicket.allProxies') }}</label>
          <label class="flex cursor-pointer items-center gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600"><input v-model="mode" type="radio" value="custom" class="text-primary-600" />{{ t('admin.accounts.codexTicket.selectedProxies') }}</label>
        </div>
        <div v-if="mode === 'custom'" class="max-h-72 space-y-1 overflow-y-auto rounded-xl border border-gray-200 p-2 dark:border-dark-600">
          <label v-for="proxy in proxies" :key="proxy.id" class="flex cursor-pointer items-center justify-between gap-3 rounded-lg px-3 py-2 hover:bg-gray-50 dark:hover:bg-dark-700">
            <span class="flex items-center gap-2"><input v-model="selected" type="checkbox" :value="proxy.id" class="rounded text-primary-600" /><span class="text-sm font-medium">{{ proxy.name }}</span></span>
            <span class="text-xs text-gray-500">{{ proxy.host }}:{{ proxy.port }}</span>
          </label>
          <p v-if="!proxies.length" class="px-3 py-5 text-center text-sm text-gray-500">{{ t('admin.accounts.codexTicket.noProxy') }}</p>
        </div>
        <p v-if="error" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      </template>
    </div>
    <template #footer><button class="btn btn-secondary" @click="$emit('close')">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="loading || saving || (mode === 'custom' && !selected.length)" @click="save">{{ saving ? t('admin.accounts.codexTicket.saving') : t('common.save') }}</button></template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getAll, getCodexTicketPool, updateCodexTicketPool } from '@/api/admin/proxies'
import type { Proxy } from '@/types'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const mode = ref<'all' | 'custom'>('all')
const selected = ref<number[]>([])
const proxies = ref<Proxy[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')
watch(() => props.show, async visible => {
  if (!visible) return
  loading.value = true
  error.value = ''
  try {
    const [pool, all] = await Promise.all([getCodexTicketPool(), getAll()])
    mode.value = pool.mode
    selected.value = pool.proxy_ids
    proxies.value = all.filter(proxy => proxy.status === 'active' && (!proxy.expires_at || new Date(proxy.expires_at).getTime() > Date.now()))
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
  finally { loading.value = false }
})
async function save() {
  saving.value = true
  error.value = ''
  try {
    await updateCodexTicketPool({ mode: mode.value, proxy_ids: mode.value === 'all' ? [] : selected.value })
    emit('saved')
    emit('close')
  } catch (cause) { error.value = cause instanceof Error ? cause.message : String(cause) }
  finally { saving.value = false }
}
</script>
