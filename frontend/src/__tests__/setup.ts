/**
 * Vitest 测试环境设置
 * 提供全局 mock 和测试工具
 */
import { config } from '@vue/test-utils'
import { beforeEach, vi } from 'vitest'

type StorageState = Map<string, string>

const createMemoryStorage = (state: StorageState): Storage => ({
  get length() {
    return state.size
  },
  clear() {
    state.clear()
  },
  getItem(key: string) {
    return state.has(key) ? state.get(key)! : null
  },
  key(index: number) {
    return Array.from(state.keys())[index] ?? null
  },
  removeItem(key: string) {
    state.delete(key)
  },
  setItem(key: string, value: string) {
    state.set(String(key), String(value))
  },
})

const installStorageMock = (name: 'localStorage' | 'sessionStorage') => {
  const state: StorageState = new Map()
  const storage = createMemoryStorage(state)
  Object.defineProperty(globalThis, name, {
    value: storage,
    configurable: true,
    writable: true,
  })
  return state
}

let localStorageState = installStorageMock('localStorage')
let sessionStorageState = installStorageMock('sessionStorage')

// Mock requestIdleCallback (Safari < 15 不支持)
if (typeof globalThis.requestIdleCallback === 'undefined') {
  globalThis.requestIdleCallback = ((callback: IdleRequestCallback) => {
    return window.setTimeout(() => callback({ didTimeout: false, timeRemaining: () => 50 }), 1)
  }) as unknown as typeof requestIdleCallback
}

if (typeof globalThis.cancelIdleCallback === 'undefined') {
  globalThis.cancelIdleCallback = ((id: number) => {
    window.clearTimeout(id)
  }) as unknown as typeof cancelIdleCallback
}

// Mock IntersectionObserver
class MockIntersectionObserver {
  observe = vi.fn()
  disconnect = vi.fn()
  unobserve = vi.fn()
}

globalThis.IntersectionObserver = MockIntersectionObserver as unknown as typeof IntersectionObserver

// Mock ResizeObserver
class MockResizeObserver {
  observe = vi.fn()
  disconnect = vi.fn()
  unobserve = vi.fn()
}

globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver

// Vue Test Utils 全局配置
config.global.stubs = {
  // 可以在这里添加全局 stub
}

beforeEach(() => {
  localStorageState.clear()
  sessionStorageState.clear()

  const local = globalThis.localStorage as Partial<Storage> | undefined
  if (!local || typeof local.clear !== 'function' || typeof local.getItem !== 'function' || typeof local.setItem !== 'function' || typeof local.removeItem !== 'function') {
    localStorageState = installStorageMock('localStorage')
  }

  const session = globalThis.sessionStorage as Partial<Storage> | undefined
  if (!session || typeof session.clear !== 'function' || typeof session.getItem !== 'function' || typeof session.setItem !== 'function' || typeof session.removeItem !== 'function') {
    sessionStorageState = installStorageMock('sessionStorage')
  }
})

// 设置全局测试超时
vi.setConfig({ testTimeout: 10000 })
