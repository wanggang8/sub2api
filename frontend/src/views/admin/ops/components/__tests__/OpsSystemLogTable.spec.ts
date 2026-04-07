import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import OpsSystemLogTable from '../OpsSystemLogTable.vue'

const mockListSystemLogs = vi.fn()
const mockGetSystemLogSinkHealth = vi.fn()
const mockGetRuntimeLogConfig = vi.fn()
const mockCleanupSystemLogs = vi.fn()
const mockUpdateRuntimeLogConfig = vi.fn()
const mockResetRuntimeLogConfig = vi.fn()
const mockShowError = vi.fn()
const mockShowSuccess = vi.fn()

vi.mock('@/api/admin/ops', () => ({
  opsAPI: {
    listSystemLogs: (...args: any[]) => mockListSystemLogs(...args),
    getSystemLogSinkHealth: (...args: any[]) => mockGetSystemLogSinkHealth(...args),
    getRuntimeLogConfig: (...args: any[]) => mockGetRuntimeLogConfig(...args),
    cleanupSystemLogs: (...args: any[]) => mockCleanupSystemLogs(...args),
    updateRuntimeLogConfig: (...args: any[]) => mockUpdateRuntimeLogConfig(...args),
    resetRuntimeLogConfig: (...args: any[]) => mockResetRuntimeLogConfig(...args),
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError: (...args: any[]) => mockShowError(...args),
    showSuccess: (...args: any[]) => mockShowSuccess(...args),
  }),
}))

const SelectStub = defineComponent({
  name: 'Select',
  props: {
    modelValue: { type: [String, Number], default: '' },
    options: { type: Array, default: () => [] },
  },
  emits: ['update:modelValue'],
  template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>',
})

const PaginationStub = defineComponent({
  name: 'Pagination',
  props: {
    page: { type: Number, default: 1 },
    pageSize: { type: Number, default: 20 },
    total: { type: Number, default: 0 },
  },
  emits: ['change', 'update:pageSize'],
  template: '<div class="pagination-stub" />',
})

describe('OpsSystemLogTable', () => {
  const confirmSpy = vi.spyOn(window, 'confirm')
  const fixedNow = new Date('2026-04-07T12:00:00.000Z')

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(fixedNow)
    vi.clearAllMocks()
    confirmSpy.mockReturnValue(true)
    mockListSystemLogs.mockResolvedValue({ items: [], total: 0 })
    mockGetSystemLogSinkHealth.mockResolvedValue({
      queue_depth: 0,
      queue_capacity: 0,
      dropped_count: 0,
      write_failed_count: 0,
      written_count: 0,
      avg_write_delay_ms: 0,
    })
    mockGetRuntimeLogConfig.mockResolvedValue({
      level: 'info',
      enable_sampling: false,
      sampling_initial: 100,
      sampling_thereafter: 100,
      caller: true,
      stacktrace_level: 'error',
      retention_days: 30,
    })
    mockUpdateRuntimeLogConfig.mockResolvedValue({
      level: 'info',
      enable_sampling: false,
      sampling_initial: 100,
      sampling_thereafter: 100,
      caller: true,
      stacktrace_level: 'error',
      retention_days: 30,
    })
    mockResetRuntimeLogConfig.mockResolvedValue({
      level: 'info',
      enable_sampling: false,
      sampling_initial: 100,
      sampling_thereafter: 100,
      caller: true,
      stacktrace_level: 'error',
      retention_days: 30,
    })
    mockCleanupSystemLogs.mockResolvedValue({ deleted: 3 })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('按当前 time_range 清理时会下发推导出的起止时间', async () => {
    const wrapper = mount(OpsSystemLogTable, {
      global: {
        stubs: {
          Select: SelectStub,
          Pagination: PaginationStub,
        },
      },
    })

    await flushPromises()

    const buttons = wrapper.findAll('button')
    const cleanupButton = buttons.find((button) => button.text() === '按当前筛选清理')
    expect(cleanupButton).toBeTruthy()

    await cleanupButton!.trigger('click')
    await flushPromises()

    expect(mockCleanupSystemLogs).toHaveBeenCalledWith(
      expect.objectContaining({
        start_time: '2026-04-07T11:00:00.000Z',
        end_time: '2026-04-07T12:00:00.000Z',
      })
    )
  })
})
