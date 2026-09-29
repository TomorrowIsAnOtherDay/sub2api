<template>
  <div v-if="eligible" ref="root" class="space-y-1 text-[10px] text-gray-500 dark:text-gray-400" data-testid="sub2api-usage">
    <div class="flex items-center gap-2">
      <span :title="t('admin.accounts.sub2apiUsage.scope')">{{ t(snapshot?.data ? 'admin.accounts.sub2apiUsage.title' : 'admin.accounts.sub2apiUsage.probe') }}</span>
      <button type="button" class="text-blue-600 disabled:opacity-50 dark:text-blue-400" :disabled="loading" @click="load(true)">
        {{ t(loading ? 'common.loading' : 'common.refresh') }}
      </button>
    </div>
    <template v-if="snapshot?.data">
      <div v-for="row in totals" :key="row.label" class="flex flex-wrap gap-x-2" :data-testid="`sub2api-${row.label}`">
        <span>{{ t(`admin.accounts.sub2apiUsage.${row.label}`) }}</span>
        <span>{{ formatCompactNumber(row.value.requests) }} {{ t('admin.accounts.sub2apiUsage.requests') }}</span>
        <span>{{ formatCompactNumber(row.value.total_tokens) }} tokens</span>
        <span v-if="row.value.actual_cost != null || row.value.cost != null">${{ (row.value.actual_cost ?? row.value.cost ?? 0).toFixed(2) }}</span>
      </div>
      <div v-for="quota in quotas" :key="quota.label" class="space-y-0.5">
        <UsageProgressBar :label="quota.label" :utilization="quota.used / quota.limit * 100" :resets-at="quota.reset_at ?? null" color="indigo" />
        <div>{{ quota.used.toFixed(2) }} / {{ quota.limit.toFixed(2) }} {{ quota.unit || 'USD' }}</div>
      </div>
      <!-- Simple mode can report balance=0 for a usable key; never show it as exhausted. -->
      <div v-if="snapshot.data.balance != null && snapshot.data.balance > 0">
        {{ t('admin.accounts.sub2apiUsage.balance') }}: {{ snapshot.data.balance.toFixed(2) }} {{ snapshot.data.unit || 'USD' }}
      </div>
      <div v-if="expiresAt">{{ t('admin.accounts.sub2apiUsage.expires') }}: {{ dateTime(expiresAt) }}</div>
      <div v-if="!snapshot.data.isValid" class="text-amber-600">{{ t('admin.accounts.sub2apiUsage.invalidKey') }}</div>
      <div v-if="snapshot.data.status && snapshot.data.status !== 'active'">{{ t('admin.accounts.sub2apiUsage.keyStatus') }}: {{ snapshot.data.status }}</div>
      <div v-if="snapshot.updated_at" :title="t('admin.accounts.sub2apiUsage.scope')">
        {{ t(snapshot.stale ? 'admin.accounts.sub2apiUsage.stale' : 'admin.accounts.sub2apiUsage.updated') }}: {{ dateTime(snapshot.updated_at) }}
      </div>
    </template>
    <div v-if="error || snapshot?.status === 'error'" role="status" class="text-amber-600">
      {{ t('admin.accounts.sub2apiUsage.unavailable') }}<span v-if="snapshot?.http_status"> (HTTP {{ snapshot.http_status }})</span>
    </div>
    <div v-else-if="snapshot?.status === 'unsupported'" role="status">{{ t('admin.accounts.sub2apiUsage.unsupported') }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { Account, Sub2APIUsageQuota, Sub2APIUsageSnapshot, Sub2APIUsageTotals } from '@/types'
import { formatCompactNumber } from '@/utils/format'
import { isSub2APIUsageCandidate } from '@/utils/sub2apiUsage'
import UsageProgressBar from './UsageProgressBar.vue'

const props = defineProps<{ account: Account; manualRefreshToken?: number }>()
const { t } = useI18n()
const root = ref<HTMLElement | null>(null)
const snapshot = ref<Sub2APIUsageSnapshot | null>(null)
const loading = ref(false)
const error = ref(false)
const visible = ref(false)
let observer: IntersectionObserver | undefined
let timer: ReturnType<typeof setInterval> | undefined
let generation = 0
let disposed = false

const eligible = computed(() => isSub2APIUsageCandidate(props.account))

const totals = computed(() => {
  const usage = snapshot.value?.data?.usage
  return (['today', 'total'] as const).flatMap(label => usage?.[label] ? [{ label, value: usage[label] as Sub2APIUsageTotals }] : [])
})
const quotas = computed(() => {
  const data = snapshot.value?.data
  const rows: (Sub2APIUsageQuota & { label: string })[] = []
  if (data?.quota) rows.push({ ...data.quota, label: t('admin.accounts.sub2apiUsage.quota') })
  for (const quota of data?.rate_limits ?? []) rows.push({ ...quota, label: quota.window ?? '' })
  const sub = data?.subscription
  if (sub) {
    for (const [period, label] of [['daily', '1d'], ['weekly', '7d'], ['monthly', '1mo']] as const) {
      const limit = sub[`${period}_limit_usd`]
      if (limit != null) rows.push({ label, limit, used: sub[`${period}_usage_usd`], remaining: 0, unit: 'USD' })
    }
  }
  return rows.filter(row => row.limit > 0)
})
const expiresAt = computed(() => snapshot.value?.data?.expires_at ?? snapshot.value?.data?.subscription?.expires_at)
const dateTime = (value: string) => new Date(value).toLocaleString()

async function load(force = false) {
  if (!eligible.value || loading.value || disposed) return
  const requestGeneration = generation
  loading.value = true
  error.value = false
  try {
    const result = await adminAPI.accounts.getUsage(props.account.id, 'active', force)
    if (!disposed && requestGeneration === generation) {
      snapshot.value = result.sub2api_usage ?? { status: 'unsupported', checked_at: new Date().toISOString(), stale: false }
    }
  } catch {
    if (!disposed && requestGeneration === generation) {
      error.value = true
      if (snapshot.value?.data) snapshot.value = { ...snapshot.value, stale: true }
    }
  } finally {
    if (!disposed && requestGeneration === generation) loading.value = false
  }
}

function observe() {
  observer?.disconnect()
  visible.value = false
  if (!eligible.value || !root.value) return
  if (typeof IntersectionObserver === 'undefined') { visible.value = true; void load(); return }
  observer = new IntersectionObserver(entries => {
    visible.value = entries.some(entry => entry.isIntersecting)
    if (visible.value) void load()
  }, { rootMargin: '100px' })
  observer.observe(root.value)
}

onMounted(() => {
  observe()
  timer = setInterval(() => { if (visible.value) void load() }, 60_000)
})
watch(() => [props.account.id, props.account.updated_at, props.account.credentials?.base_url, eligible.value], () => {
  generation++
  snapshot.value = null
  loading.value = false
  error.value = false
  observe()
}, { flush: 'post' })
watch(() => props.manualRefreshToken, (next, previous) => {
  if (next !== previous && visible.value) void load(true)
})
onBeforeUnmount(() => { disposed = true; generation++; observer?.disconnect(); if (timer) clearInterval(timer) })
</script>
