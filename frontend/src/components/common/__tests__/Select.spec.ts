import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { mount } from '@vue/test-utils'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import Select from '../Select.vue'

const SelectHarness = defineComponent({
  components: { Select },
  setup() {
    const first = ref<string | null>(null)
    const second = ref<string | null>(null)
    const options = [
      { value: 'a', label: 'Option A' },
      { value: 'b', label: 'Option B' }
    ]

    return {
      first,
      second,
      options
    }
  },
  template: `
    <div>
      <Select v-model="first" :options="options" searchable />
      <Select v-model="second" :options="options" searchable />
    </div>
  `
})

describe('Select', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('closes the previously opened dropdown when another select is opened', async () => {
    const wrapper = mount(SelectHarness, {
      attachTo: document.body,
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    const triggers = wrapper.findAll('button[aria-label="Select option"]')

    await triggers[0]!.trigger('click')
    expect(document.body.querySelectorAll('.select-dropdown-portal')).toHaveLength(1)
    expect(triggers[0]!.attributes('aria-expanded')).toBe('true')

    await triggers[1]!.trigger('click')

    expect(document.body.querySelectorAll('.select-dropdown-portal')).toHaveLength(1)
    expect(triggers[0]!.attributes('aria-expanded')).toBe('false')
    expect(triggers[1]!.attributes('aria-expanded')).toBe('true')
  })
})
