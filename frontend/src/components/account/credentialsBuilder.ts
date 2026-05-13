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
    default:
      return 'https://api.anthropic.com'
  }
}

export function isCustomPlatformBaseURL(platform: string, baseURL: string): boolean {
  const trimmed = baseURL.trim()
  if (!trimmed) {
    return false
  }

  const defaultBaseURL = getDefaultAPIKeyBaseURL(platform).toLowerCase().replace(/\/+$/, '')

  try {
    const parsed = new URL(trimmed)
    const origin = `${parsed.protocol.toLowerCase()}//${parsed.hostname.toLowerCase()}`
    const pathname = parsed.pathname.toLowerCase().replace(/\/+$/, '')
    const normalizedParsed = `${origin}${pathname}`
    return normalizedParsed !== defaultBaseURL && normalizedParsed !== `${defaultBaseURL}/v1`
  } catch {
    // Fall back to normalized string comparison below for malformed or partial values.
  }

  const normalized = trimmed.toLowerCase().replace(/\/+$/, '')
  return normalized !== defaultBaseURL && normalized !== `${defaultBaseURL}/v1`
}

export function isCustomOpenAIBaseURL(baseURL: string): boolean {
  return isCustomPlatformBaseURL('openai', baseURL)
}
