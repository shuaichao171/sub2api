<template>
  <BaseDialog :show="show" :title="`${t('admin.accounts.codexTicket.title')} · ${modelLabel}`" width="extra-wide" @close="$emit('close')">
    <div v-if="account" class="space-y-5">
      <div class="flex flex-wrap items-start justify-between gap-3 rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800">
        <div class="space-y-1">
          <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ account.name }} <span class="font-normal text-gray-500">#{{ account.id }}</span></p>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.current') }}: <span :class="status?.ready && !status?.reusing_expired ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">{{ status?.ready ? (status?.reusing_expired ? t('admin.accounts.codexTicket.reusedExpired') : t('admin.accounts.codexTicket.valid')) : t('admin.accounts.codexTicket.missing') }}</span><span v-if="status?.expires_at"> · {{ formatDateTime(status.expires_at) }} {{ timezone }}</span></p>
          <p v-if="status?.harvest_paused" class="text-xs text-amber-600 dark:text-amber-400">{{ t('admin.accounts.codexTicket.autoPaused') }} {{ status.harvest_resume_at ? formatDateTime(status.harvest_resume_at) : '' }}</p>
          <p v-if="lastSuccess" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.lastSuccess') }}: {{ formatDateTime(lastSuccess.occurred_at) }} {{ timezone }}</p>
        </div>
        <button class="btn btn-primary" :disabled="running || !manualAvailable" :title="!manualAvailable ? unavailableText : undefined" @click="runManual">
          <Icon name="refresh" size="sm" :class="['mr-1.5', running ? 'animate-spin' : '']" />{{ running ? t('admin.accounts.codexTicket.retrying') : t('admin.accounts.codexTicket.retry') }}
        </button>
      </div>

      <p v-if="!manualAvailable && unavailableText" class="text-xs text-amber-600 dark:text-amber-400">{{ unavailableText }}</p>
      <p v-if="feedback" class="rounded-lg border border-primary-200 bg-primary-50 px-3 py-2 text-sm text-primary-800 dark:border-primary-900 dark:bg-primary-900/20 dark:text-primary-200" role="status">{{ feedback }}</p>

      <div class="flex flex-wrap items-center gap-4 rounded-xl border border-gray-200 px-4 py-3 text-xs text-gray-600 dark:border-dark-600 dark:text-gray-300">
        <label class="flex items-center gap-2"><input v-model="accountEnabled" type="checkbox" class="rounded text-primary-600" :disabled="savingPolicy" @change="savePolicy" />{{ t('admin.accounts.codexTicket.accountEnabled') }}</label>
        <label class="flex items-center gap-2"><input v-model="modelEnabled" type="checkbox" class="rounded text-primary-600" :disabled="savingPolicy" @change="savePolicy" />{{ t('admin.accounts.codexTicket.modelEnabled') }}</label>
      </div>

      <div class="flex items-center justify-between border-b border-gray-200 dark:border-dark-600">
        <div class="flex gap-5">
          <button v-for="item in tabs" :key="item.key" class="border-b-2 pb-2 text-sm font-medium transition-colors" :class="tab === item.key ? 'border-primary-500 text-primary-700 dark:text-primary-300' : 'border-transparent text-gray-500 hover:text-gray-800 dark:hover:text-gray-200'" @click="tab = item.key">{{ item.label }}</button>
        </div>
        <button class="mb-2 rounded-lg p-1.5 text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-700" :title="t('common.refresh')" @click="load"><Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" /></button>
      </div>

      <div v-if="loadError" class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900 dark:bg-red-900/20 dark:text-red-300">{{ loadError }}</div>
      <div v-else-if="loading" class="py-10 text-center text-sm text-gray-500">{{ t('admin.accounts.codexTicket.loading') }}</div>
      <div v-else-if="!current.items.length" class="py-10 text-center text-sm text-gray-500">{{ t('admin.accounts.codexTicket.empty') }}</div>
      <div v-else class="divide-y divide-gray-200/80 rounded-xl border border-gray-200 bg-white dark:divide-dark-600 dark:border-dark-600 dark:bg-dark-800">
        <div v-for="(item, index) in current.items" :key="item.id" class="px-4 py-3.5">
          <p v-if="tab === 'success' && (index === 0 || localDate(current.items[index - 1].occurred_at) !== localDate(item.occurred_at))" class="mb-2 text-xs font-semibold text-primary-700 dark:text-primary-300">{{ localDate(item.occurred_at) }}</p>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex items-center gap-2 text-sm">
              <span class="h-2 w-2 rounded-full" :class="item.outcome === 'success' ? 'bg-emerald-500' : item.outcome === 'miss' ? 'bg-amber-500' : 'bg-red-500'" />
              <span class="font-medium text-gray-900 dark:text-gray-100">{{ t(`admin.accounts.codexTicket.${item.outcome}`) }}</span>
              <span v-if="item.trigger === 'manual'" class="badge badge-primary text-[10px]">{{ t('admin.accounts.codexTicket.manual') }}</span>
            </div>
            <time class="text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ formatDateTime(item.occurred_at) }} {{ timezone }}</time>
          </div>
          <div v-if="tab === 'all'" class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
            <span>HTTP {{ item.http_status ?? '—' }}</span><span>{{ t('admin.accounts.codexTicket.length') }} {{ item.ticket_length ?? '—' }}</span>
            <span>{{ item.duration_ms }} ms</span><span>{{ t('admin.accounts.codexTicket.proxy') }} {{ item.proxy_name || '—' }}</span>
            <span v-if="item.reason_code" class="text-amber-600 dark:text-amber-400">{{ t('admin.accounts.codexTicket.reason') }}: {{ item.reason_code }}</span>
          </div>
          <p v-else-if="item.proxy_name" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.proxy') }} {{ item.proxy_name }}</p>
        </div>
      </div>

      <div class="flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
        <span>{{ t('admin.accounts.codexTicket.total', { count: current.total }) }}</span>
        <div class="flex items-center gap-3"><button class="btn btn-secondary btn-sm" :disabled="current.page <= 1 || loading" @click="changePage(-1)">‹</button><span>{{ current.page }} / {{ Math.max(1, Math.ceil(current.total / 20)) }}</span><button class="btn btn-secondary btn-sm" :disabled="current.page * 20 >= current.total || loading" @click="changePage(1)">›</button></div>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import * as accountAPI from '@/api/admin/accounts'
import type { AccountListItem } from '@/types'
import type { CodexTicketHistory, CodexTicketAttempt, CodexTicketStatus } from '@/api/admin/accounts'

const props = defineProps<{ show: boolean; account: AccountListItem | null; model: string }>()
const emit = defineEmits<{ close: []; refreshed: [] }>()
const { t } = useI18n()
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
const tab = ref<'all' | 'success'>('all')
const pages = ref({ all: 1, success: 1 })
const data = ref<Record<'all' | 'success', CodexTicketHistory | null>>({ all: null, success: null })
const current = computed(() => data.value[tab.value] ?? { items: [] as CodexTicketAttempt[], total: 0, page: pages.value[tab.value] })
const status = computed<CodexTicketStatus | undefined>(() => data.value[tab.value]?.ticket_status ?? props.account?.codex_turn_tickets?.find(ticket => ticket.model === props.model))
const lastSuccess = ref<CodexTicketAttempt | null>(null)
const manualAvailable = computed(() => data.value[tab.value]?.manual_available ?? false)
const unavailableText = computed(() => {
  const reason = data.value[tab.value]?.manual_unavailable_reason
  return reason === 'no_proxy' ? t('admin.accounts.codexTicket.noProxy') : reason === 'disabled' ? t('admin.accounts.codexTicket.disabled') : ''
})
const modelLabel = computed(() => props.model === 'gpt-6-astra' ? '6 Astra' : '5.6 Sol')
const tabs = computed(() => [{ key: 'all' as const, label: t('admin.accounts.codexTicket.attempts') }, { key: 'success' as const, label: t('admin.accounts.codexTicket.successes') }])
const loading = ref(false)
const running = ref(false)
const savingPolicy = ref(false)
const feedback = ref('')
const loadError = ref('')
const accountEnabled = ref(true)
const modelEnabled = ref(true)
let requestVersion = 0

const localDate = (value: string) => new Intl.DateTimeFormat(undefined, { year: 'numeric', month: 'long', day: 'numeric' }).format(new Date(value))
async function load() {
  if (!props.show || !props.account || !props.model) return
  const version = ++requestVersion
  const key = tab.value
  loading.value = true
  loadError.value = ''
  try {
    const response = await accountAPI.getCodexTicketHistory(props.account.id, props.model, key, pages.value[key])
    if (version === requestVersion) {
      data.value = { ...data.value, [key]: response }
      if (key === 'success' && pages.value.success === 1) lastSuccess.value = response.items[0] ?? null
      if (key === 'all' && !data.value.success) {
        const successes = await accountAPI.getCodexTicketHistory(props.account.id, props.model, 'success', 1)
        if (version === requestVersion) {
          data.value = { ...data.value, success: successes }
          lastSuccess.value = successes.items[0] ?? null
        }
      }
    }
  } catch (error) { if (version === requestVersion) loadError.value = error instanceof Error ? error.message : String(error) }
  finally { if (version === requestVersion) loading.value = false }
}
function changePage(delta: number) { pages.value[tab.value] += delta; load() }
async function runManual() {
  if (!props.account || running.value) return
  running.value = true
  feedback.value = ''
  try {
    const result = await accountAPI.harvestCodexTicket(props.account.id, props.model)
    feedback.value = t(`admin.accounts.codexTicket.${result.outcome}`) + (result.history_recorded ? '' : ` · ${t('admin.accounts.codexTicket.historyFailed')}`)
    data.value = { all: null, success: null }
    lastSuccess.value = null
    pages.value = { all: 1, success: 1 }
    emit('refreshed')
    await load()
  } catch (error) { feedback.value = error instanceof Error ? error.message : String(error) }
  finally { running.value = false }
}
async function savePolicy() {
  if (!props.account || savingPolicy.value) return
  savingPolicy.value = true
  try {
    const existing = (props.account.extra?.codex_ticket_harvest_models ?? {}) as Record<string, boolean>
    await accountAPI.setCodexTicketParticipation(props.account.id, accountEnabled.value, { ...existing, [props.model]: modelEnabled.value })
    feedback.value = t('admin.accounts.codexTicket.policySaved')
    emit('refreshed')
    await load()
  } catch (error) { feedback.value = error instanceof Error ? error.message : String(error) }
  finally { savingPolicy.value = false }
}
watch(() => [props.show, props.account?.id, props.model] as const, () => {
  requestVersion++
  if (!props.show || !props.account) return
  data.value = { all: null, success: null }
  lastSuccess.value = null
  pages.value = { all: 1, success: 1 }
  tab.value = 'all'
  feedback.value = ''
  accountEnabled.value = props.account.extra?.codex_ticket_harvest_enabled !== false
  const models = props.account.extra?.codex_ticket_harvest_models as Record<string, boolean> | undefined
  modelEnabled.value = models?.[props.model] !== false
  load()
}, { immediate: true })
watch(tab, () => { if (!data.value[tab.value]) load() })
</script>
