import { describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { updateAccountMock, checkMixedChannelRiskMock, listTLSFingerprintProfilesMock, fetchModelsPreviewMock, getAvailableModelsMock, showSuccessMock, showWarningMock, showErrorMock } = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  listTLSFingerprintProfilesMock: vi.fn(),
  fetchModelsPreviewMock: vi.fn(),
  getAvailableModelsMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showWarningMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.hoisted(() => {
  Object.defineProperty(globalThis, 'localStorage', {
    value: {
      getItem: vi.fn(),
      setItem: vi.fn(),
      removeItem: vi.fn()
    },
    configurable: true
  })
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: showSuccessMock,
    showInfo: vi.fn(),
    showWarning: showWarningMock
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    isSimpleMode: true
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      update: updateAccountMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock
    },
    tlsFingerprintProfiles: {
      list: listTLSFingerprintProfilesMock
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn(),
  fetchModelsPreview: fetchModelsPreviewMock,
  getAvailableModels: getAvailableModelsMock
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import EditAccountModal from '../EditAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <div>
      <button
        type="button"
        data-testid="rewrite-to-snapshot"
        @click="$emit('update:modelValue', ['gpt-5.2-2025-12-11'])"
      >
        rewrite
      </button>
      <span data-testid="model-whitelist-value">
        {{ Array.isArray(modelValue) ? modelValue.join(',') : '' }}
      </span>
    </div>
  `
})

function buildAccount() {
  return {
    id: 1,
    name: 'OpenAI Key',
    notes: '',
    platform: 'openai',
    type: 'apikey',
    credentials: {
      api_key: 'sk-test',
      base_url: 'https://relay.example.com',
      model_mapping: {
        'gpt-5.2': 'gpt-5.2'
      }
    },
    extra: {
      custom_base_url_enabled: true,
      custom_base_url: 'https://relay.example.com',
      openai_upstream_supports_responses: true,
      openai_upstream_supports_chat_completions: true,
      openai_upstream_supports_messages: false
    },
    openai_upstream_supports_responses: true,
    openai_upstream_supports_chat_completions: true,
    openai_upstream_supports_messages: false,
    custom_base_url_enabled: true,
    custom_base_url: 'https://relay.example.com',
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildAnthropicOAuthAccount() {
  return {
    id: 2,
    name: 'Claude OAuth',
    notes: '',
    platform: 'anthropic',
    type: 'oauth',
    credentials: {},
    extra: {
      tls_insecure_skip_verify: true
    },
    tls_insecure_skip_verify: true,
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildOpenAIOAuthAccount() {
  return {
    id: 3,
    name: 'OpenAI OAuth',
    notes: '',
    platform: 'openai',
    type: 'oauth',
    credentials: {},
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildOfficialOpenAIApiKeyAccount() {
  return {
    ...buildAccount(),
    credentials: {
      api_key: 'sk-test',
      base_url: 'https://api.openai.com',
      model_mapping: {
        'gpt-5.2': 'gpt-5.2'
      }
    },
    extra: {},
    openai_upstream_supports_responses: true,
    openai_upstream_supports_chat_completions: false,
    openai_upstream_supports_messages: false,
    custom_base_url_enabled: false,
    custom_base_url: null
  } as any
}

function buildOfficialOpenAIV1ApiKeyAccount() {
  return {
    ...buildOfficialOpenAIApiKeyAccount(),
    credentials: {
      api_key: 'sk-test',
      base_url: 'https://api.openai.com/v1',
      model_mapping: {
        'gpt-5.2': 'gpt-5.2'
      }
    }
  } as any
}

function buildOpenAICursorCompatAccount() {
  return {
    ...buildAccount(),
    extra: {
      cursor_session_compat_enabled: true
    },
  } as any
}

function mountModal(account = buildAccount()) {
  return mount(EditAccountModal, {
    props: {
      show: true,
      account,
      proxies: [],
      groups: []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: true,
        Icon: true,
        ProxySelector: true,
        GroupSelector: true,
        ModelWhitelistSelector: ModelWhitelistSelectorStub
      }
    }
  })
}

describe('EditAccountModal', () => {
  it('shows fetch models button for anthropic api-key mapping and loads available models', async () => {
    const account = {
      ...buildAccount(),
      platform: 'anthropic',
      type: 'apikey',
      credentials: {
        api_key: 'sk-anthropic',
        base_url: 'https://api.anthropic.com',
        model_mapping: {}
      }
    }

    fetchModelsPreviewMock.mockReset()
    getAvailableModelsMock.mockReset()
    listTLSFingerprintProfilesMock.mockReset()
    showWarningMock.mockReset()
    fetchModelsPreviewMock.mockResolvedValue({ models: [{ id: 'claude-sonnet-4-6' }], source: 'default' })
    listTLSFingerprintProfilesMock.mockResolvedValue([])

    const wrapper = mountModal(account)
    const vm = wrapper.vm as any
    vm.modelRestrictionMode = 'mapping'
    await flushPromises()

    const fetchButton = wrapper.findAll('button').find((button) =>
      button.text().includes('admin.accounts.fetchModels') ||
      button.text().includes('admin.accounts.refreshModels')
    )

    expect(fetchButton?.exists()).toBe(true)

    await fetchButton!.trigger('click')

    expect(fetchModelsPreviewMock).toHaveBeenCalledWith(account.id, expect.objectContaining({
      platform: 'anthropic',
      type: 'apikey',
      credentials: expect.objectContaining({
        base_url: 'https://api.anthropic.com',
        api_key: 'sk-anthropic'
      })
    }))
    expect(showWarningMock).toHaveBeenCalled()
  })

  it('reopening the same account rehydrates the OpenAI whitelist from props', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    listTLSFingerprintProfilesMock.mockReset()
    listTLSFingerprintProfilesMock.mockResolvedValue([])
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2-2025-12-11')

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-5.2': 'gpt-5.2'
    })
  })

  it('renders and persists tls_insecure_skip_verify for anthropic oauth accounts', async () => {
    const account = buildAnthropicOAuthAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    listTLSFingerprintProfilesMock.mockReset()
    listTLSFingerprintProfilesMock.mockResolvedValue([])
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await flushPromises()

    expect(wrapper.find('[data-testid="tls-insecure-skip-verify-toggle"]').exists()).toBe(true)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.tls_insecure_skip_verify).toBe(true)

    await wrapper.get('[data-testid="tls-insecure-skip-verify-toggle"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(2)
    expect(updateAccountMock.mock.calls[1]?.[1]?.extra?.tls_insecure_skip_verify).toBeUndefined()
  })

  it('clears fetched model cache when switching to another account', async () => {
    const accountA = buildAccount()
    const accountB = {
      ...buildAccount(),
      id: 9,
      name: 'Other OpenAI Key',
      credentials: {
        api_key: 'sk-test-b',
        base_url: 'https://example.com/v1',
        model_mapping: {
          'gpt-4.1': 'gpt-4.1'
        }
      }
    }

    fetchModelsPreviewMock.mockReset()
    getAvailableModelsMock.mockReset()
    listTLSFingerprintProfilesMock.mockReset()
    listTLSFingerprintProfilesMock.mockResolvedValue([])
    fetchModelsPreviewMock.mockResolvedValue({ models: [{ id: 'gpt-a' }], source: 'upstream' })

    const wrapper = mountModal(accountA)
    await wrapper.setData?.({})
    await wrapper.setProps({ account: accountA })
    await flushPromises()

    await wrapper.setProps({ account: accountB })
    await flushPromises()

    fetchModelsPreviewMock.mockResolvedValue({ models: [{ id: 'gpt-b' }], source: 'upstream' })
    const vm = wrapper.vm as any
    await vm.fetchAccountModels(false)

    expect(fetchModelsPreviewMock).toHaveBeenLastCalledWith(accountB.id, expect.objectContaining({
      credentials: expect.objectContaining({
        base_url: 'https://example.com/v1'
      })
    }))
  })

  it('does not render legacy cursor session compat toggle for openai accounts', async () => {
    const account = buildOpenAICursorCompatAccount()
    listTLSFingerprintProfilesMock.mockReset()
    listTLSFingerprintProfilesMock.mockResolvedValue([])

    const wrapper = mountModal(account)
    await flushPromises()

    expect(wrapper.find('[data-testid="cursor-session-compat-toggle"]').exists()).toBe(false)
  })

  it('does not render OpenAI upstream capabilities for oauth accounts', async () => {
    const account = buildOpenAIOAuthAccount()
    listTLSFingerprintProfilesMock.mockResolvedValue([])

    const wrapper = mountModal(account)
    await flushPromises()

    expect(wrapper.text()).not.toContain('admin.accounts.openai.upstreamCapabilities')
  })

  it('does not render OpenAI upstream capabilities for official OpenAI api-key accounts', async () => {
    const account = buildOfficialOpenAIApiKeyAccount()
    listTLSFingerprintProfilesMock.mockResolvedValue([])

    const wrapper = mountModal(account)
    await flushPromises()

    expect(wrapper.text()).not.toContain('admin.accounts.openai.upstreamCapabilities')
  })

  it('does not render OpenAI upstream capabilities for official OpenAI v1 api-key accounts', async () => {
    const account = buildOfficialOpenAIV1ApiKeyAccount()
    listTLSFingerprintProfilesMock.mockResolvedValue([])

    const wrapper = mountModal(account)
    await flushPromises()

    expect(wrapper.text()).not.toContain('admin.accounts.openai.upstreamCapabilities')
  })

  it('persists updated OpenAI upstream capabilities', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    listTLSFingerprintProfilesMock.mockReset()
    listTLSFingerprintProfilesMock.mockResolvedValue([])
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const vm = wrapper.vm as any
    await flushPromises()

    vm.editBaseUrl = 'https://relay.example.com'
    vm.openAIUpstreamCapabilityPresets[2].apply()
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_upstream_supports_responses).toBe(false)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_upstream_supports_chat_completions).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_upstream_supports_messages).toBe(true)
  })

  it('rejects saving when all OpenAI upstream capabilities are disabled', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    showErrorMock.mockReset()
    listTLSFingerprintProfilesMock.mockResolvedValue([])

    const wrapper = mountModal(account)
    const vm = wrapper.vm as any
    await flushPromises()

    vm.editBaseUrl = 'https://relay.example.com'
    vm.openaiUpstreamSupportsResponses = false
    vm.openaiUpstreamSupportsChatCompletions = false
    vm.openaiUpstreamSupportsMessages = false
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalled()
  })

  it('clears OpenAI upstream capabilities when custom base url is disabled', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    listTLSFingerprintProfilesMock.mockReset()
    listTLSFingerprintProfilesMock.mockResolvedValue([])
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const vm = wrapper.vm as any
    await flushPromises()

    vm.customBaseUrlEnabled = false
    vm.customBaseUrl = ''
    vm.editBaseUrl = 'https://api.openai.com'
    vm.openaiUpstreamSupportsResponses = false
    vm.openaiUpstreamSupportsChatCompletions = true
    vm.openaiUpstreamSupportsMessages = true
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_upstream_supports_responses).toBeUndefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_upstream_supports_chat_completions).toBeUndefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_upstream_supports_messages).toBeUndefined()
  })
})
