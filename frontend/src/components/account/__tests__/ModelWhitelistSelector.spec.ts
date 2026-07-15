import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { accountsAPI } from '@/api/admin/accounts'
import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: {
    syncUpstreamModels: vi.fn(),
    syncUpstreamModelsPreview: vi.fn()
  }
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

describe('ModelWhitelistSelector', () => {
  beforeEach(() => {
    vi.mocked(accountsAPI.syncUpstreamModelsPreview).mockReset()
  })

  it('previews upstream models with create-flow URL and TLS credentials', async () => {
    vi.mocked(accountsAPI.syncUpstreamModelsPreview).mockResolvedValue({
      models: ['gpt-preview']
    })
    const syncCredentials = {
      platform: 'openai',
      type: 'apikey',
      base_url: 'https://openai.example.test',
      api_key: 'sk-preview',
      skip_tls_verify: true
    }
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        platform: 'openai',
        syncCredentials
      },
      global: {
        stubs: {
          ModelIcon: true,
          Icon: true
        }
      }
    })

    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')
    expect(syncButton).toBeTruthy()
    await syncButton!.trigger('click')
    await flushPromises()

    expect(accountsAPI.syncUpstreamModelsPreview).toHaveBeenCalledWith(syncCredentials)
    expect(wrapper.emitted('update:modelValue')).toContainEqual([['gpt-preview']])
  })
})
