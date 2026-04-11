import { describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const {
  createAccountMock,
  checkMixedChannelRiskMock,
  listTLSFingerprintProfilesMock,
  fetchModelsPreviewMock,
  getAntigravityDefaultModelMappingMock,
  showSuccessMock,
  showWarningMock,
  showErrorMock
} = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  listTLSFingerprintProfilesMock: vi.fn(),
  fetchModelsPreviewMock: vi.fn(),
  getAntigravityDefaultModelMappingMock: vi.fn(),
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

function buildOAuthComposableMock() {
  return {
    authUrl: ref(''),
    sessionId: ref(''),
    loading: ref(false),
    error: ref(''),
    oauthState: ref(''),
    resetState: vi.fn(),
    generateAuthUrl: vi.fn(),
    exchangeAuthCode: vi.fn(),
    buildCredentials: vi.fn(() => ({})),
    buildExtraInfo: vi.fn(() => ({})),
    validateRefreshToken: vi.fn(),
    parseSessionKeys: vi.fn(() => ({}))
  }
}

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
      create: createAccountMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock,
      exchangeCode: vi.fn()
    },
    tlsFingerprintProfiles: {
      list: listTLSFingerprintProfilesMock
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: getAntigravityDefaultModelMappingMock,
  fetchModelsPreview: fetchModelsPreviewMock
}))

vi.mock('@/composables/useAccountOAuth', () => ({
  useAccountOAuth: () => buildOAuthComposableMock()
}))

vi.mock('@/composables/useOpenAIOAuth', () => ({
  useOpenAIOAuth: () => buildOAuthComposableMock()
}))

vi.mock('@/composables/useGeminiOAuth', () => ({
  useGeminiOAuth: () => ({
    ...buildOAuthComposableMock(),
    getCapabilities: vi.fn().mockResolvedValue({ ai_studio_oauth_enabled: true })
  })
}))

vi.mock('@/composables/useAntigravityOAuth', () => ({
  useAntigravityOAuth: () => buildOAuthComposableMock()
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

import CreateAccountModal from '../CreateAccountModal.vue'

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

function mountModal() {
  return mount(CreateAccountModal, {
    props: {
      show: true,
      proxies: [],
      groups: []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        Select: true,
        Icon: true,
        ProxySelector: true,
        GroupSelector: true,
        ModelWhitelistSelector: true,
        QuotaLimitCard: true,
        OAuthAuthorizationFlow: true
      }
    }
  })
}

describe('CreateAccountModal', () => {
  it('shows fetch models button in mapping mode for api-key accounts and previews current form models', async () => {
    fetchModelsPreviewMock.mockReset()
    getAntigravityDefaultModelMappingMock.mockReset()
    listTLSFingerprintProfilesMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    listTLSFingerprintProfilesMock.mockResolvedValue([])
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    getAntigravityDefaultModelMappingMock.mockResolvedValue({})
    fetchModelsPreviewMock.mockResolvedValue({ models: [{ id: 'gpt-5.4' }], source: 'upstream' })

    const wrapper = mountModal()
    const vm = wrapper.vm as any

    vm.form.platform = 'openai'
    vm.accountCategory = 'apikey'
    vm.form.type = 'apikey'
    vm.modelRestrictionMode = 'mapping'
    vm.apiKeyValue = 'sk-preview'
    await flushPromises()

    const fetchButton = wrapper.findAll('button').find((button) =>
      button.text().includes('admin.accounts.fetchModels') ||
      button.text().includes('admin.accounts.refreshModels')
    )

    expect(fetchButton?.exists()).toBe(true)

    await fetchButton!.trigger('click')

    expect(fetchModelsPreviewMock).toHaveBeenCalledWith(0, {
      platform: 'openai',
      type: 'apikey',
      credentials: {
        base_url: 'https://api.openai.com',
        api_key: 'sk-preview'
      }
    })
  })

  it('shows fetch models button for anthropic api-key mapping and falls back to default models', async () => {
    fetchModelsPreviewMock.mockReset()
    getAntigravityDefaultModelMappingMock.mockReset()
    listTLSFingerprintProfilesMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    showWarningMock.mockReset()
    fetchModelsPreviewMock.mockResolvedValue({ models: [{ id: 'claude-sonnet-4-6' }], source: 'default' })
    listTLSFingerprintProfilesMock.mockResolvedValue([])
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    getAntigravityDefaultModelMappingMock.mockResolvedValue({})

    const wrapper = mountModal()
    const vm = wrapper.vm as any

    vm.form.platform = 'anthropic'
    vm.accountCategory = 'apikey'
    vm.form.type = 'apikey'
    vm.modelRestrictionMode = 'mapping'
    vm.apiKeyValue = 'sk-anthropic'
    await flushPromises()

    expect(vm.supportsModelPreview).toBe(true)

    await vm.fetchAccountModels(false)

    expect(fetchModelsPreviewMock).toHaveBeenCalledWith(0, {
      platform: 'anthropic',
      type: 'apikey',
      credentials: {
        base_url: 'https://api.anthropic.com',
        api_key: 'sk-anthropic',
      }
    })
    expect(showWarningMock).toHaveBeenCalled()
  })
})
