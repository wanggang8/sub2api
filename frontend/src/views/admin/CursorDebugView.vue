<template>
  <AppLayout>
    <div class="mx-auto flex w-full max-w-7xl flex-col gap-5 p-4 sm:p-6">
      <div class="flex flex-col gap-4 rounded-lg border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">Cursor Debug</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.cursorDebug.subtitle', 'Inspect Cursor request snapshots and export a complete capture.') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <label class="inline-flex items-center gap-2 rounded border border-gray-200 px-3 py-2 text-sm dark:border-dark-600">
            <input
              type="checkbox"
              class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="config.enabled"
              :disabled="configLoading"
              @change="toggleEnabled(($event.target as HTMLInputElement).checked)"
            />
            <span class="text-gray-700 dark:text-gray-200">{{ t('admin.cursorDebug.enabled', 'Capture enabled') }}</span>
          </label>
          <button class="btn btn-secondary" :disabled="loading" @click="refreshAll">
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button class="btn btn-secondary text-red-600 hover:text-red-700 dark:text-red-400" :disabled="clearing || records.length === 0" @click="clearRecords">
            <Icon name="trash" size="md" />
            {{ t('common.clear', 'Clear') }}
          </button>
        </div>
      </div>

      <div class="grid gap-5 xl:grid-cols-[380px_1fr]">
        <section class="rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <div class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
            <div>
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.cursorDebug.records', 'Records') }}</h2>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ total }} {{ t('common.total', 'total') }}</p>
            </div>
            <select v-model.number="pageSize" class="input h-9 w-24 text-sm" @change="loadRecords">
              <option :value="10">10</option>
              <option :value="20">20</option>
              <option :value="50">50</option>
            </select>
          </div>
          <div v-if="loading" class="p-6 text-sm text-gray-500 dark:text-gray-400">
            {{ t('common.loading', 'Loading...') }}
          </div>
          <div v-else-if="records.length === 0" class="p-6 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.cursorDebug.empty', 'No captures yet.') }}
          </div>
          <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
            <button
              v-for="record in records"
              :key="record.id"
              type="button"
              class="block w-full px-4 py-3 text-left transition hover:bg-gray-50 dark:hover:bg-dark-700/70"
              :class="selected?.id === record.id ? 'bg-primary-50 dark:bg-primary-900/20' : ''"
              @click="selectRecord(record.id)"
            >
              <div class="flex items-center justify-between gap-3">
                <span class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ record.model || '-' }}</span>
                <span class="shrink-0 rounded bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-dark-600 dark:text-gray-300">
                  {{ record.stream ? 'SSE' : 'JSON' }}
                </span>
              </div>
              <div class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">{{ record.path }}</div>
              <div class="mt-2 flex items-center justify-between gap-2 text-xs text-gray-400">
                <span>{{ formatDate(record.created_at) }}</span>
                <span>{{ record.platform || '-' }}</span>
              </div>
            </button>
          </div>
          <div class="flex items-center justify-between border-t border-gray-200 px-4 py-3 dark:border-dark-700">
            <button class="btn btn-secondary btn-sm" :disabled="page <= 1" @click="changePage(page - 1)">
              <Icon name="chevronLeft" size="sm" />
            </button>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ page }} / {{ totalPages }}</span>
            <button class="btn btn-secondary btn-sm" :disabled="page >= totalPages" @click="changePage(page + 1)">
              <Icon name="chevronRight" size="sm" />
            </button>
          </div>
        </section>

        <section class="rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <div v-if="!selected" class="flex min-h-[520px] items-center justify-center p-8 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.cursorDebug.selectRecord', 'Select a record to inspect.') }}
          </div>
          <div v-else class="flex flex-col">
            <div class="flex flex-col gap-3 border-b border-gray-200 p-4 dark:border-dark-700 lg:flex-row lg:items-start lg:justify-between">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ selected.model || '-' }}</h2>
                  <span class="rounded bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-dark-600 dark:text-gray-300">{{ selected.platform || '-' }}</span>
                  <span class="rounded bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-dark-600 dark:text-gray-300">{{ selected.status_code || '-' }}</span>
                </div>
                <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">{{ selected.request_id || selected.id }}</p>
              </div>
              <div class="flex flex-wrap items-center gap-2">
                <button class="btn btn-secondary btn-sm" @click="copyRecord">
                  <Icon name="copy" size="sm" />
                  {{ t('common.copy', 'Copy') }}
                </button>
                <button class="btn btn-primary btn-sm" @click="downloadRecord">
                  <Icon name="download" size="sm" />
                  {{ t('common.export', 'Export') }}
                </button>
              </div>
            </div>

            <div class="grid gap-4 p-4">
              <SnapshotBlock title="Raw Request" :snapshot="selected.raw_request_body" mode="json" @copy="copyText" @download="downloadSnapshot" />
              <SnapshotBlock title="Normalized Request" :snapshot="selected.normalized_request_body" mode="json" @copy="copyText" @download="downloadSnapshot" />
              <SnapshotBlock title="Upstream Request" :snapshot="selected.upstream_request_body" mode="json" @copy="copyText" @download="downloadSnapshot" />
              <SnapshotBlock title="Raw Upstream Response / SSE" :snapshot="selected.raw_upstream_response_body" mode="text" @copy="copyText" @download="downloadSnapshot" />
              <SnapshotBlock title="Final Response / SSE" :snapshot="selected.final_response_body" mode="text" @copy="copyText" @download="downloadSnapshot" />
            </div>
          </div>
        </section>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { opsAPI, type CursorDebugBody, type CursorDebugConfig, type CursorDebugRecord } from '@/api/admin/ops'
import { useAppStore } from '@/stores'
import { useClipboard } from '@/composables/useClipboard'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const config = ref<CursorDebugConfig>({
  enabled: false,
  max_records: 100,
  max_body_bytes: 512 * 1024,
  retention_hours: 6
})
const records = ref<CursorDebugRecord[]>([])
const selected = ref<CursorDebugRecord | null>(null)
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const configLoading = ref(false)
const clearing = ref(false)

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))

const SnapshotBlock = defineComponent({
  props: {
    title: { type: String, required: true },
    snapshot: { type: Object as () => CursorDebugBody, required: true },
    mode: { type: String, default: 'text' }
  },
  emits: ['copy', 'download'],
  setup(props, { emit }) {
    const open = ref(true)
    const displayBody = computed(() => {
      if (props.mode !== 'json') return props.snapshot.body || ''
      try {
        return JSON.stringify(JSON.parse(props.snapshot.body || '{}'), null, 2)
      } catch {
        return props.snapshot.body || ''
      }
    })
    return () => h('section', { class: 'overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700' }, [
      h('div', { class: 'flex flex-wrap items-center justify-between gap-2 bg-gray-50 px-3 py-2 dark:bg-dark-700/60' }, [
        h('button', { type: 'button', class: 'flex items-center gap-2 text-sm font-medium text-gray-900 dark:text-white', onClick: () => { open.value = !open.value } }, [
          h(Icon, { name: open.value ? 'chevronDown' : 'chevronRight', size: 'sm' }),
          props.title,
          props.snapshot.truncated ? h('span', { class: 'rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-700 dark:bg-amber-900/30 dark:text-amber-300' }, 'truncated') : null
        ]),
        h('div', { class: 'flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400' }, [
          h('span', formatBytes(props.snapshot.bytes)),
          h('button', { type: 'button', class: 'rounded p-1 hover:bg-gray-200 dark:hover:bg-dark-600', onClick: () => emit('copy', displayBody.value) }, [h(Icon, { name: 'copy', size: 'sm' })]),
          h('button', { type: 'button', class: 'rounded p-1 hover:bg-gray-200 dark:hover:bg-dark-600', onClick: () => emit('download', props.title, displayBody.value) }, [h(Icon, { name: 'download', size: 'sm' })])
        ])
      ]),
      open.value ? h('pre', { class: 'max-h-[360px] overflow-auto whitespace-pre-wrap break-words bg-gray-950 p-3 text-xs leading-relaxed text-gray-100' }, displayBody.value || '(empty)') : null
    ])
  }
})

async function loadConfig() {
  configLoading.value = true
  try {
    config.value = await opsAPI.getCursorDebugConfig()
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to load Cursor debug config')
  } finally {
    configLoading.value = false
  }
}

async function loadRecords() {
  loading.value = true
  try {
    const res = await opsAPI.listCursorDebugRecords({ page: page.value, page_size: pageSize.value })
    records.value = res.items || []
    total.value = res.total || 0
    if (selected.value) {
      const fresh = records.value.find((item) => item.id === selected.value?.id)
      if (fresh) selected.value = fresh
    } else if (records.value.length > 0) {
      selected.value = records.value[0]
    }
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to load Cursor debug records')
  } finally {
    loading.value = false
  }
}

async function refreshAll() {
  await Promise.all([loadConfig(), loadRecords()])
}

async function toggleEnabled(enabled: boolean) {
  configLoading.value = true
  try {
    config.value = await opsAPI.updateCursorDebugConfig({ enabled })
    appStore.showSuccess(enabled ? 'Cursor debug capture enabled' : 'Cursor debug capture disabled')
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to update Cursor debug config')
  } finally {
    configLoading.value = false
  }
}

async function selectRecord(id: string) {
  try {
    selected.value = await opsAPI.getCursorDebugRecord(id)
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to load Cursor debug record')
  }
}

async function clearRecords() {
  clearing.value = true
  try {
    const res = await opsAPI.clearCursorDebugRecords()
    records.value = []
    selected.value = null
    total.value = 0
    appStore.showSuccess(`Cleared ${res.deleted} Cursor debug records`)
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to clear Cursor debug records')
  } finally {
    clearing.value = false
  }
}

async function copyRecord() {
  if (!selected.value) return
  await copyToClipboard(JSON.stringify(selected.value, null, 2), 'Cursor debug record copied')
}

async function downloadRecord() {
  if (!selected.value) return
  const record = await opsAPI.exportCursorDebugRecord(selected.value.id)
  downloadText(`cursor-debug-${record.id}.json`, JSON.stringify(record, null, 2), 'application/json')
}

async function copyText(text: string) {
  await copyToClipboard(text, 'Snapshot copied')
}

function downloadSnapshot(title: string, text: string) {
  const suffix = title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'snapshot'
  downloadText(`cursor-debug-${selected.value?.id || 'record'}-${suffix}.txt`, text, 'text/plain')
}

function downloadText(filename: string, content: string, type: string) {
  const blob = new Blob([content], { type })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

function changePage(next: number) {
  page.value = Math.min(Math.max(1, next), totalPages.value)
  loadRecords()
}

function formatDate(value?: string) {
  if (!value) return '-'
  return new Date(value).toLocaleString()
}

function formatBytes(bytes?: number) {
  const value = bytes || 0
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(2)} MB`
}

onMounted(refreshAll)
</script>
