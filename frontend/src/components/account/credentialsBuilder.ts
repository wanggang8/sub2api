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

export function isCustomOpenAIBaseURL(baseURL: string): boolean {
  const trimmed = baseURL.trim()
  if (!trimmed) {
    return false
  }

  try {
    const parsed = new URL(trimmed)
    if (parsed.protocol.toLowerCase() === 'https:' && parsed.hostname.toLowerCase() === 'api.openai.com') {
      return false
    }
  } catch {
    // Fall back to normalized string comparison below for malformed or partial values.
  }

  const normalized = trimmed.toLowerCase().replace(/\/+$/, '')
  return normalized !== 'https://api.openai.com' && normalized !== 'https://api.openai.com/v1'
}
