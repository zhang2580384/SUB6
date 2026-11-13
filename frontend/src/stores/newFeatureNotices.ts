import { defineStore } from 'pinia'
import { ref } from 'vue'

const STORAGE_PREFIX = 'sub2api:new-feature-notices:v1:'

export const useNewFeatureNoticeStore = defineStore('newFeatureNotices', () => {
  const dismissedByUser = ref<Record<string, string[]>>({})
  const loadedUsers = ref<Record<string, true>>({})

  function userKey(userId: number): string {
    return String(userId)
  }

  function ensureLoaded(userId: number): string {
    const key = userKey(userId)
    if (loadedUsers.value[key]) return key

    let dismissed: string[] = []
    try {
      const raw = localStorage.getItem(`${STORAGE_PREFIX}${key}`)
      const parsed: unknown = raw ? JSON.parse(raw) : []
      if (Array.isArray(parsed)) {
        dismissed = parsed.filter((id): id is string => typeof id === 'string')
      }
    } catch {
      // A malformed or unavailable local preference must not hide new notices.
    }

    dismissedByUser.value = { ...dismissedByUser.value, [key]: dismissed }
    loadedUsers.value = { ...loadedUsers.value, [key]: true }
    return key
  }

  // Revision 1 preserves existing preferences. A later announcement of the same
  // feature uses a new explicit revision rather than resetting every release.
  function noticeKey(noticeId: string, version = '1'): string {
    return version === '1' ? noticeId : `${noticeId}::version=${encodeURIComponent(version)}`
  }

  function isDismissed(userId: number | null | undefined, noticeId: string, version = '1'): boolean {
    if (userId == null || !noticeId) return false
    const key = ensureLoaded(userId)
    return dismissedByUser.value[key]?.includes(noticeKey(noticeId, version)) ?? false
  }

  function loadUser(userId: number | null | undefined): void {
    if (userId != null) ensureLoaded(userId)
  }

  function dismiss(userId: number | null | undefined, noticeId: string, version = '1'): void {
    if (userId == null || !noticeId || isDismissed(userId, noticeId, version)) return
    const key = userKey(userId)
    const dismissed = [...(dismissedByUser.value[key] ?? []), noticeKey(noticeId, version)]
    dismissedByUser.value = { ...dismissedByUser.value, [key]: dismissed }

    try {
      localStorage.setItem(`${STORAGE_PREFIX}${key}`, JSON.stringify(dismissed))
    } catch {
      // The current session still hides the notice if persistence is unavailable.
    }
  }

  return { isDismissed, loadUser, dismiss }
})
