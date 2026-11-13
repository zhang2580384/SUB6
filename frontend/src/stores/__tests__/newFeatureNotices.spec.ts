import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NEW_FEATURE_NOTICES } from '@/constants/newFeatureNotices'
import { useNewFeatureNoticeStore } from '../newFeatureNotices'

describe('new feature notice preferences', () => {
  afterEach(() => vi.restoreAllMocks())
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('persists dismissed notice IDs per admin account', () => {
    const store = useNewFeatureNoticeStore()
    expect(store.isDismissed(41, 'protocol-switches')).toBe(false)

    store.dismiss(41, 'protocol-switches')

    expect(store.isDismissed(41, 'protocol-switches')).toBe(true)
    expect(store.isDismissed(42, 'protocol-switches')).toBe(false)
    expect(localStorage.getItem('sub2api:new-feature-notices:v1:41')).toBe('["protocol-switches"]')

    setActivePinia(createPinia())
    expect(useNewFeatureNoticeStore().isDismissed(41, 'protocol-switches')).toBe(true)
  })

  it('ignores malformed stored preferences and does not overwrite other notices', () => {
    localStorage.setItem('sub2api:new-feature-notices:v1:41', '{bad json')
    const store = useNewFeatureNoticeStore()

    expect(store.isDismissed(41, 'protocol-switches')).toBe(false)
    store.dismiss(41, 'protocol-switches')
    store.dismiss(41, 'other-feature')

    expect(JSON.parse(localStorage.getItem('sub2api:new-feature-notices:v1:41') ?? '[]')).toEqual([
      'protocol-switches',
      'other-feature',
    ])
  })

  it('isolates announcement versions, users and future notices across reloads', () => {
    const store = useNewFeatureNoticeStore()
    store.dismiss(41, 'feature')
    expect(store.isDismissed(41, 'feature', '2')).toBe(false)
    store.dismiss(41, 'feature', '2')
    setActivePinia(createPinia())
    const restored = useNewFeatureNoticeStore()
    expect(restored.isDismissed(41, 'feature')).toBe(true)
    expect(restored.isDismissed(41, 'feature', '2')).toBe(true)
    expect(restored.isDismissed(41, 'feature', '3')).toBe(false)
    expect(restored.isDismissed(41, 'future')).toBe(false)
    expect(restored.isDismissed(42, 'feature', '2')).toBe(false)
    expect(restored.isDismissed(null, 'feature')).toBe(false)
  })

  it('keeps current-session reads usable when browser storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('unavailable') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('unavailable') })
    const store = useNewFeatureNoticeStore()
    expect(store.isDismissed(41, 'feature')).toBe(false)
    expect(() => store.dismiss(41, 'feature')).not.toThrow()
    expect(store.isDismissed(41, 'feature')).toBe(true)
    expect(store.isDismissed(41, 'future')).toBe(false)
  })
  it('announces key source restrictions independently and preserves dismissals across reloads', () => {
    const store = useNewFeatureNoticeStore()
    store.dismiss(41, NEW_FEATURE_NOTICES.keyRisk)
    expect(store.isDismissed(41, NEW_FEATURE_NOTICES.keyRiskSources)).toBe(false)
    store.dismiss(41, NEW_FEATURE_NOTICES.keyRiskSources)
    expect(store.isDismissed(41, NEW_FEATURE_NOTICES.keyRiskSources)).toBe(true)
    setActivePinia(createPinia())
    const reloaded = useNewFeatureNoticeStore()
    expect(reloaded.isDismissed(41, NEW_FEATURE_NOTICES.keyRisk)).toBe(true)
    expect(reloaded.isDismissed(41, NEW_FEATURE_NOTICES.keyRiskSources)).toBe(true)
    expect(reloaded.isDismissed(41, 'future-key-risk-feature')).toBe(false)
    expect(reloaded.isDismissed(42, NEW_FEATURE_NOTICES.keyRiskSources)).toBe(false)
  })
})
