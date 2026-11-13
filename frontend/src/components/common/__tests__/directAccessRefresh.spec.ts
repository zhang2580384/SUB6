import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'

const sidecar = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../../deploy/direct-access/static')
const source = readFileSync(resolve(sidecar, 'app.js'), 'utf8')
const page = readFileSync(resolve(sidecar, 'index.html'), 'utf8')
const listing = { entries: [], current_ip: '', direct_url: 'https://direct.example.com',
  limit: 1, total_recharged: '100', admin_credit_total: '0', manual_limit: null,
  is_admin: false, sync_ok: true }

describe('direct-access status refresh', () => {
  beforeEach(() => {
    document.body.innerHTML = new DOMParser().parseFromString(page, 'text/html').body.innerHTML
    localStorage.setItem('auth_token', 'synthetic-session')
  })
  afterEach(() => { document.body.innerHTML = ''; localStorage.clear(); vi.restoreAllMocks() })

  it('clears an auth error after a successful refresh and preserves unfinished input', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce({ status: 401, ok: false, json: async () => ({ error: 'Login expired' }) })
      .mockResolvedValue({ status: 200, ok: true, json: async () => listing })
    new Function('document', 'location', 'localStorage', 'fetch', 'setInterval', source)(
      document, { search: '' }, localStorage, fetch, vi.fn())
    await flushPromises()
    expect(document.getElementById('message')?.textContent).toBe('Login expired')
    const ip = document.getElementById('ip') as HTMLInputElement
    const label = document.getElementById('label') as HTMLInputElement
    ip.value = '192.0.2.20'; label.value = 'unfinished input'
    document.getElementById('refresh')!.click()
    await flushPromises()
    expect(document.getElementById('message')?.classList.contains('error')).toBe(false)
    expect(document.getElementById('message')?.textContent).toBe('状态已更新')
    expect(ip.value).toBe('192.0.2.20')
    expect(label.value).toBe('unfinished input')
    expect(fetch.mock.calls.map(call => call[1].method)).toEqual([undefined, undefined])
  })

  it('removes a stale synchronizer warning after a healthy automatic refresh', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce({ status: 200, ok: true, json: async () => ({ ...listing, sync_ok: false }) })
      .mockResolvedValue({ status: 200, ok: true, json: async () => listing })
    const interval = vi.fn()
    new Function('document', 'location', 'localStorage', 'fetch', 'setInterval', source)(
      document, { search: '' }, localStorage, fetch, interval)
    await flushPromises()
    expect(document.getElementById('message')?.classList.contains('error')).toBe(true)
    interval.mock.calls[0]![0]()
    await flushPromises()
    expect((document.getElementById('message') as HTMLElement).hidden).toBe(true)
  })
})
