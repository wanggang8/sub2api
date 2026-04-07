import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import OpsErrorDetailModal from '../OpsErrorDetailModal.vue'

const mockGetRequestErrorDetail = vi.fn()
const mockGetUpstreamErrorDetail = vi.fn()
const mockListRequestErrorUpstreamErrors = vi.fn()
const mockShowError = vi.fn()

vi.mock('@/api/admin/ops', () => ({
  opsAPI: {
    getRequestErrorDetail: (...args: any[]) => mockGetRequestErrorDetail(...args),
    getUpstreamErrorDetail: (...args: any[]) => mockGetUpstreamErrorDetail(...args),
    listRequestErrorUpstreamErrors: (...args: any[]) => mockListRequestErrorUpstreamErrors(...args),
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError: (...args: any[]) => mockShowError(...args),
  }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, any>) => {
        if (params?.id) return `${key}:${params.id}`
        return key
      },
    }),
  }
})

vi.mock('@/utils/format', () => ({
  formatDateTime: (value: string) => value,
}))

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: { type: Boolean, default: false },
    title: { type: String, default: '' },
  },
  emits: ['close'],
  template: '<div v-if="show"><div class="dialog-title">{{ title }}</div><slot /></div>',
})

const IconStub = defineComponent({
  name: 'Icon',
  template: '<span class="icon-stub" />',
})

describe('OpsErrorDetailModal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListRequestErrorUpstreamErrors.mockResolvedValue({ items: [] })
  })

  it('renders request replay payload, request headers, upstream status and timing diagnostics', async () => {
    mockGetRequestErrorDetail.mockResolvedValue({
      id: 9,
      created_at: '2026-04-07T12:00:00Z',
      phase: 'request',
      type: 'api_error',
      error_owner: 'platform',
      error_source: 'gateway',
      severity: 'P1',
      status_code: 200,
      platform: 'openai',
      model: 'gpt-5.4-mini',
      is_retryable: true,
      retry_count: 0,
      resolved: false,
      client_request_id: 'client-rid',
      request_id: 'rid-123',
      message: 'Upstream request failed',
      user_email: 'user@example.com',
      account_name: 'acc-1',
      group_name: 'group-1',
      error_body: '{"type":"error","error":{"type":"upstream_error","message":"Upstream request failed"}}',
      user_agent: 'augment',
      upstream_status_code: 502,
      upstream_error_detail: '{"message":"EOF"}',
      auth_latency_ms: 11,
      routing_latency_ms: 22,
      upstream_latency_ms: 33,
      response_latency_ms: 44,
      time_to_first_token_ms: 55,
      request_body: '{"prompt":"hello"}',
      request_body_truncated: true,
      request_body_bytes: 2048,
      request_headers: '{"x-trace-id":"abc"}',
      is_business_limited: false,
      inbound_endpoint: '/chat-stream',
      upstream_endpoint: '/backend-api/codex/responses',
    })

    const wrapper = mount(OpsErrorDetailModal, {
      props: {
        show: true,
        errorId: 9,
        errorType: 'request',
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Icon: IconStub,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('"prompt": "hello"')
    expect(wrapper.text()).toContain('"x-trace-id": "abc"')
    expect(wrapper.text()).toContain('2048')
    expect(wrapper.text()).toContain('502')
    expect(wrapper.text()).toContain('55 ms')
  })
})
