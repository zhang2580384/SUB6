import { mount, flushPromises } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import zhMisc from '@/i18n/locales/zh/misc'
import VersionBadge from '../VersionBadge.vue'

const mocks = vi.hoisted(() => ({
  admin: true,
  buildType: 'release',
  fetchVersion: vi.fn(),
  clearVersionCache: vi.fn(),
  performUpdate: vi.fn(),
  restartService: vi.fn(),
  getUpdateStatus: vi.fn(),
  installation: undefined as { managed: boolean; allowed: boolean; reason_code: string } | undefined,
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ isAdmin: mocks.admin }),
  useAppStore: () => ({
    currentVersion: '0.1.2', latestVersion: '0.1.3', hasUpdate: true,
    versionLoading: false, buildType: mocks.buildType,
    installation: mocks.installation,
    releaseInfo: { html_url: 'https://github.com/zhang2580384/SUB6/releases/tag/v0.1.3' },
    fetchVersion: mocks.fetchVersion, clearVersionCache: mocks.clearVersionCache,
  }),
}))
vi.mock('@/api/admin/system', () => ({
  performUpdate: mocks.performUpdate, restartService: mocks.restartService,
  getRollbackVersions: vi.fn(), rollback: vi.fn(),
  getUpdateStatus: mocks.getUpdateStatus,
}))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() }),
}))

describe('VersionBadge update flow', () => {
  let wrapper: ReturnType<typeof mount> | undefined

  beforeEach(() => {
    vi.clearAllMocks()
    mocks.admin = true
    mocks.buildType = 'release'
    mocks.installation = undefined
    mocks.fetchVersion.mockResolvedValue(undefined)
    mocks.performUpdate.mockResolvedValue({ need_restart: true })
    mocks.getUpdateStatus.mockResolvedValue({ managed: false, phase: 'idle' })
  })
  afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

  async function openBadge() {
    wrapper = mount(VersionBadge, { global: {
      plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: zhMisc } })],
      stubs: { Icon: true },
    } })
    await flushPromises()
    await wrapper.get('button').trigger('click')
    return wrapper
  }

  it('shows the migration reason and never starts an update rejected by preflight', async () => {
    mocks.installation = { managed: true, allowed: false, reason_code: 'migration_review_required' }
    const badge = await openBadge()
    expect(badge.text()).toContain('尚未获准在线迁移')
    const blocked = badge.findAll('button').find(button => button.text() === '暂不能自动更新')!
    expect(blocked.attributes('disabled')).toBeDefined()
    await blocked.trigger('click')
    expect(mocks.performUpdate).not.toHaveBeenCalled()
  })

  it('fails closed when the controller cannot check compatibility', async () => {
    mocks.installation = { managed: true, allowed: false, reason_code: 'controller_preflight_unavailable' }
    const badge = await openBadge()
    expect(badge.text()).toContain('暂时无法确认升级兼容性')
    expect(badge.findAll('button').some(button => button.text() === '立即更新')).toBe(false)
  })

  it('waits for all managed services and never offers an in-place restart', async () => {
    vi.useFakeTimers()
    const badge = await openBadge()
    mocks.performUpdate.mockResolvedValue({ managed: true, operation_id: 'rollout', need_restart: false })
    mocks.getUpdateStatus
      .mockResolvedValueOnce({ managed: true, operation_id: 'rollout', phase: 'draining' })
      .mockRejectedValueOnce(new Error('brief cutover'))
      .mockResolvedValueOnce({ managed: true, operation_id: 'rollout', phase: 'completed' })
    await badge.findAll('button').find(button => button.text() === '立即更新')!.trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1500)
    expect(badge.text()).toContain('正在等待已有请求完成')
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(badge.text()).toContain('更新完成')
    expect(badge.text()).not.toContain('立即重启')
    expect(mocks.restartService).not.toHaveBeenCalled()
    expect(mocks.fetchVersion).toHaveBeenCalledWith(true)
  })

  it('shows a managed rollout failure without claiming completion', async () => {
    vi.useFakeTimers()
    const badge = await openBadge()
    mocks.performUpdate.mockResolvedValue({ managed: true, operation_id: 'failed-rollout', need_restart: false })
    mocks.getUpdateStatus.mockResolvedValue({ managed: true, operation_id: 'failed-rollout', phase: 'failed', error: 'Candidate readiness failed' })
    await badge.findAll('button').find(button => button.text() === '立即更新')!.trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1500)
    expect(badge.text()).toContain('Candidate readiness failed')
    expect(badge.text()).not.toContain('更新完成')
  })

  it('shows the original immediate-update button for packaged builds and asks for restart after success', async () => {
    const badge = await openBadge()
    const update = badge.findAll('button').find(button => button.text() === '立即更新')
    expect(update).toBeDefined()
    await update!.trigger('click')
    await flushPromises()
    expect(mocks.performUpdate).toHaveBeenCalledOnce()
    expect(mocks.clearVersionCache).toHaveBeenCalledOnce()
    expect(badge.text()).toContain('立即重启')
    expect(mocks.restartService).not.toHaveBeenCalled()
  })

  it('keeps a failed update visible and does not offer restart as if installation succeeded', async () => {
    mocks.performUpdate.mockRejectedValueOnce({ response: { data: { message: '发行包校验失败' } } })
    const badge = await openBadge()
    await badge.findAll('button').find(button => button.text() === '立即更新')!.trigger('click')
    await flushPromises()
    expect(badge.text()).toContain('发行包校验失败')
    expect(badge.text()).toContain('重试')
    expect(badge.text()).not.toContain('立即重启')
    expect(mocks.clearVersionCache).not.toHaveBeenCalled()
  })

  it('retains manual upgrade guidance for a genuine local source build', async () => {
    mocks.buildType = 'source'
    const badge = await openBadge()
    expect(badge.findAll('button').some(button => button.text() === '立即更新')).toBe(false)
    expect(badge.text()).toContain(zhMisc.version.sourceModeHint)
  })

  it('does not expose administrative updates to a non-admin', async () => {
    mocks.admin = false
    wrapper = mount(VersionBadge, { global: {
      plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: zhMisc } })],
      stubs: { Icon: true },
    } })
    expect(wrapper.find('button').exists()).toBe(false)
    expect(mocks.fetchVersion).not.toHaveBeenCalled()
  })
})
