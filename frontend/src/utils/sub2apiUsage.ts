import type { Account } from '@/types'

// Detection is completed by the server; this only avoids probing known official
// providers and accounts that cannot carry a Sub2API key.
export function isSub2APIUsageCandidate(account: Account): boolean {
  if (account.platform !== 'openai' || account.type !== 'apikey' || account.extra?.sub2api_usage_enabled === false) return false
  const base = account.credentials?.base_url
  if (typeof base !== 'string' || !base.trim()) return false
  try {
    const host = new URL(base).hostname.toLowerCase().replace(/\.$/, '')
    return !['openai.com', 'anthropic.com', 'googleapis.com', 'x.ai', 'grok.com', 'ollama.com', 'moonshot.cn', 'kimi.com', 'bigmodel.cn', 'deepseek.com', 'opencode.ai'].some(domain => host === domain || host.endsWith(`.${domain}`))
  } catch { return false }
}
