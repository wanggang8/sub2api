import { describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { mount } from '@vue/test-utils'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
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
      create: vi.fn(),
      checkMixedChannelRisk: vi.fn()
    },
    settings: {
      getSettings: vi.fn().mockResolvedValue({ account_quota_notify_enabled: false }),
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false }),
      getImageGenerationConfig: vi.fn().mockResolvedValue({ enabled: false })
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([])
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([])
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

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  template: '<div />'
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
  template: '<div />'
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
        ModelWhitelistSelector: ModelWhitelistSelectorStub,
        QuotaLimitCard: true,
        OAuthAuthorizationFlow: OAuthAuthorizationFlowStub
      }
    }
  })
}

async function switchToOpenAIAPIKey(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.findAll('button').find((btn) => btn.text().includes('OpenAI'))?.trigger('click')
  await nextTick()
  await wrapper.findAll('button').find((btn) => btn.text().includes('API Key'))?.trigger('click')
  await nextTick()
}

describe('CreateAccountModal', () => {
  it('shows upstream-only controls only for custom OpenAI API base URLs', async () => {
    const wrapper = mountModal()

    await switchToOpenAIAPIKey(wrapper)

    expect(wrapper.text()).not.toContain('上游模型')
    expect(wrapper.text()).not.toContain('OpenAI 上游协议')

    const baseUrlInput = wrapper.get('input[placeholder="https://api.openai.com"]')
    await baseUrlInput.setValue('https://gateway.example.com/v1')
    await nextTick()

    expect(wrapper.text()).toContain('上游模型')
    expect(wrapper.text()).toContain('OpenAI 上游协议')
    expect(wrapper.text()).toContain('读取上游模型')
  })
})
