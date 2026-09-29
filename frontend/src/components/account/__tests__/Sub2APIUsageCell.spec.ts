import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Sub2APIUsageCell from '../Sub2APIUsageCell.vue'
import type { Account, Sub2APIUsageSnapshot } from '@/types'

const { getUsage } = vi.hoisted(() => ({ getUsage: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getUsage } } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
const account = (id = 7) => ({ id, platform: 'openai', type: 'apikey', credentials: { base_url: 'https://relay.example/v1' }, updated_at: '2026-09-29T00:00:00Z' }) as Account
const snapshot = (): Sub2APIUsageSnapshot => ({
  status: 'ok', checked_at: '2026-09-29T00:00:00Z', updated_at: '2026-09-29T00:00:00Z', stale: false,
  data: { mode: 'unrestricted', isValid: true, balance: 0, usage: {
    today: { requests: 12, total_tokens: 3400, cost: 2, actual_cost: 1.25 },
    total: { requests: 20, total_tokens: 5600, actual_cost: 2.5 }
  } }
})
const wrappers: ReturnType<typeof mount>[] = []
function render(a = account()) {
  const wrapper = mount(Sub2APIUsageCell, { props: { account: a }, global: { stubs: { UsageProgressBar: true } } })
  wrappers.push(wrapper)
  return wrapper
}

beforeEach(() => {
  vi.stubGlobal('IntersectionObserver', undefined)
  getUsage.mockReset()
  getUsage.mockResolvedValue({ sub2api_usage: snapshot() })
})
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers.length = 0; vi.unstubAllGlobals() })

describe('Sub2APIUsageCell', () => {
  it('shows upstream totals and actual cost without treating a simple-mode zero balance as exhaustion', async () => {
    const wrapper = render(); await flushPromises()
    expect(getUsage).toHaveBeenCalledWith(7, 'active', false)
    expect(wrapper.get('[data-testid="sub2api-today"]').text()).toContain('$1.25')
    expect(wrapper.get('[data-testid="sub2api-total"]').text()).toContain('$2.50')
    expect(wrapper.text()).not.toContain('sub2apiUsage.balance')
    expect(wrapper.text()).not.toContain('sub2apiUsage.invalidKey')
    await wrapper.get('button').trigger('click'); await flushPromises()
    expect(getUsage).toHaveBeenLastCalledWith(7, 'active', true)
  })

  it('renders quota windows, expiry and stale data during an upstream error', async () => {
    const result = snapshot()
    result.status = 'error'; result.http_status = 403; result.stale = true
    result.data!.quota = { limit: 10, used: 5, remaining: 5 }
    result.data!.rate_limits = [{ window: '5h', limit: 2, used: 1, remaining: 1, reset_at: '2026-09-29T12:00:00Z' }]
    result.data!.subscription = { daily_limit_usd: 5, daily_usage_usd: 2, weekly_usage_usd: 0, monthly_usage_usd: 0 }
    result.data!.expires_at = '2026-10-01T00:00:00Z'
    getUsage.mockResolvedValue({ sub2api_usage: result })
    const wrapper = render(); await flushPromises()
    expect(wrapper.findAll('usage-progress-bar-stub')).toHaveLength(3)
    expect(wrapper.text()).toContain('HTTP 403')
    expect(wrapper.text()).toContain('sub2apiUsage.stale')
    expect(wrapper.text()).toContain('sub2apiUsage.expires')
    expect(wrapper.text()).toContain('$1.25')
  })

  it.each(['https://api.openai.com/v1', 'https://api.openai.com.:443/v1', 'https://ollama.com/v1', ''])('does not probe official/default hosts: %s', async base => {
    const a = account(); a.credentials = { base_url: base }
    const wrapper = render(a); await flushPromises()
    expect(getUsage).not.toHaveBeenCalled(); expect(wrapper.find('[data-testid="sub2api-usage"]').exists()).toBe(false)
  })

  it('does not probe OAuth or explicitly disabled accounts', async () => {
    const a = account(); a.type = 'oauth'; render(a)
    const disabled = account(); disabled.extra = { sub2api_usage_enabled: false }; render(disabled)
    await flushPromises(); expect(getUsage).not.toHaveBeenCalled()
  })

  it('waits for visibility before requesting usage', async () => {
    let callback: IntersectionObserverCallback | undefined
    vi.stubGlobal('IntersectionObserver', class {
      constructor(cb: IntersectionObserverCallback) { callback = cb }
      observe() {}
      disconnect() {}
    })
    render(); await flushPromises(); expect(getUsage).not.toHaveBeenCalled()
    callback!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)
    await flushPromises(); expect(getUsage).toHaveBeenCalledTimes(1)
  })

  it('discards a response for a previous account after the row is reused', async () => {
    let resolveOld: (value: unknown) => void = () => {}
    getUsage.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = render()
    await wrapper.setProps({ account: account(8) }); await flushPromises()
    resolveOld({ sub2api_usage: { ...snapshot(), status: 'unsupported', data: undefined } }); await flushPromises()
    expect(wrapper.text()).toContain('$1.25')
    expect(wrapper.text()).not.toContain('sub2apiUsage.unsupported')
  })

  it('keeps prior data visibly stale when the local API request fails', async () => {
    const wrapper = render(); await flushPromises()
    getUsage.mockRejectedValueOnce(new Error('network'))
    await wrapper.get('button').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('$1.25')
    expect(wrapper.text()).toContain('sub2apiUsage.stale')
    expect(wrapper.text()).toContain('sub2apiUsage.unavailable')
  })
})
