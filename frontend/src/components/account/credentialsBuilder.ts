export function applyInterceptWarmup(
  credentials: Record<string, unknown>,
  enabled: boolean,
  mode: 'create' | 'edit'
): void {
  if (enabled) {
    credentials.intercept_warmup_requests = true
  } else if (mode === 'edit') {
    delete credentials.intercept_warmup_requests
  }
}

export function getDefaultAPIKeyBaseURL(platform: string): string {
  switch (platform) {
    case 'openai':
      return 'https://api.openai.com'
    case 'gemini':
      return 'https://generativelanguage.googleapis.com'
    case 'grok':
      return 'https://api.x.ai/v1'
    default:
      return 'https://api.anthropic.com'
  }
}

function getDefaultAPIKeyBaseURLAliases(platform: string): string[] {
  const defaultBaseURL = getDefaultAPIKeyBaseURL(platform)
  switch (platform) {
    case 'openai':
    case 'anthropic':
      return [defaultBaseURL, `${defaultBaseURL}/v1`]
    case 'gemini':
      return [defaultBaseURL, `${defaultBaseURL}/v1beta`]
    default:
      return [defaultBaseURL]
  }
}

function normalizeBaseURLForComparison(baseURL: string): string {
  const trimmed = baseURL.trim()
  if (!trimmed) {
    return ''
  }

  try {
    const parsed = new URL(trimmed)
    const origin = `${parsed.protocol.toLowerCase()}//${parsed.host.toLowerCase()}`
    const pathname = parsed.pathname.toLowerCase().replace(/\/+$/, '')
    return `${origin}${pathname}`
  } catch {
    return trimmed.toLowerCase().replace(/\/+$/, '')
  }
}

export function isCustomPlatformBaseURL(platform: string, baseURL: string): boolean {
  const normalized = normalizeBaseURLForComparison(baseURL)
  if (!normalized) {
    return false
  }

  const defaultBaseURLs = getDefaultAPIKeyBaseURLAliases(platform)
    .map(normalizeBaseURLForComparison)
  return !defaultBaseURLs.includes(normalized)
}

export function isCustomOpenAIBaseURL(baseURL: string): boolean {
  return isCustomPlatformBaseURL('openai', baseURL)
}

export const ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY = 'antigravity_project_id'

export function applyAntigravityProjectID(
  credentials: Record<string, unknown>,
  projectId: string,
  mode: 'create' | 'edit'
): void {
  const trimmed = projectId.trim()
  if (trimmed) {
    credentials[ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY] = trimmed
  } else if (mode === 'edit') {
    delete credentials[ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY]
  }
}
