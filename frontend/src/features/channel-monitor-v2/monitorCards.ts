import type { HealthState, MonitorCandyHistory, MonitorCandyResult, MonitorCoverage, MonitorMatrixBucket, MonitorMatrixRow, MonitorMetric, MonitorRange } from '@/api/channelMonitorV2'

export const CANDY_HISTORY_LIMIT = 100
export const CANDY_HISTORY_LIMIT_MIN = 15
export const CANDY_HISTORY_LIMIT_MAX = 100
export const MONITOR_CARD_SLOT_COUNT = 18
/** Matches ChannelMonitorV2RecentSampleBucket. Sample bars are 5-minute slots, not the selected range bucket. */
export const MONITOR_CARD_SAMPLE_BUCKET_MS = 5 * 60 * 1000
export type MonitorCardTimelineMode = 'windows' | 'samples'

export function monitorCardTimelineMode(value: string | null | undefined): MonitorCardTimelineMode {
  return value === 'samples' ? 'samples' : 'windows'
}

export function candyHistoryLimit(history: { history_limit?: number } | null | undefined): number {
  const limit = history?.history_limit
  if (typeof limit === 'number' && Number.isInteger(limit) && limit >= CANDY_HISTORY_LIMIT_MIN && limit <= CANDY_HISTORY_LIMIT_MAX) return limit
  return CANDY_HISTORY_LIMIT
}

export function monitorCandyHistories(row: Pick<MonitorMatrixRow, 'candy' | 'candy_checks'>): MonitorCandyHistory[] {
  if (row.candy_checks && row.candy_checks.length > 0) return row.candy_checks
  return row.candy ? [row.candy] : []
}

export function candyHistorySlots(history: MonitorCandyHistory): Array<MonitorCandyResult | null> {
  const limit = candyHistoryLimit(history)
  const results = history.results.slice(-limit)
  return [...Array<null>(limit - results.length).fill(null), ...results]
}

export function hasMonitorSamples(metric: MonitorMetric): boolean {
  return metric.has_samples === true || metric.request_count > 0
}

// A recent-sample bar is a bucket the health score can use. Traffic below
// minimum_sample is not a sample. Redacted payloads keep has_samples and a
// scored band; overall "unknown" is the server's insufficient-sample result.
export function meetsMonitorSampleMinimum(bucket: Pick<MonitorMatrixBucket, 'metrics' | 'health'>): boolean {
  if (!bucket.metrics || !hasMonitorSamples(bucket.metrics)) return false
  const minimum = bucket.health?.minimum_sample
  const count = bucket.metrics.request_count
  if (typeof minimum === 'number' && minimum > 0 && count > 0) return count >= minimum
  if (count <= 0 && bucket.metrics.has_samples === true && bucket.health?.overall === 'unknown') return false
  return true
}

export function monitorRefreshSeconds(configSeconds: number | undefined, rows: readonly MonitorMatrixRow[], bootstrap: boolean): number {
  if (bootstrap) return 10
  let seconds = configSeconds && Number.isFinite(configSeconds) && configSeconds > 0 ? configSeconds : 300
  for (const row of rows) {
    for (const history of monitorCandyHistories(row)) {
      const interval = history.interval_minutes
      if (interval && Number.isFinite(interval) && interval > 0) {
        seconds = Math.min(seconds, interval * 60)
      }
    }
  }
  // Refresh before a healthy minute probe can appear stale in a five-minute snapshot.
  return Math.max(60, seconds)
}

export interface MonitorCardBar {
  start: number
  end: number
  state: HealthState
  observed: boolean
  buckets: MonitorMatrixBucket[]
  probe?: { model: string; result: MonitorCandyResult }
}

function coverageBounds(coverage: MonitorCoverage | undefined) {
  const start = Date.parse(coverage?.requested_start || '')
  const end = Date.parse(coverage?.requested_end || coverage?.data_through || '')
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return null
  return { start, end }
}

// Matches the native rollups returned by ChannelMonitorV2Service.ParseFilter.
const cacheWindows: Record<MonitorRange, { bucketSeconds: number; count: number }> = {
  '90m': { bucketSeconds: 5 * 60, count: 18 },
  '24h': { bucketSeconds: 3600, count: 24 },
  '7d': { bucketSeconds: 12 * 3600, count: 14 },
  '30d': { bucketSeconds: 24 * 3600, count: 30 },
}

/** Nearest-rank P90 of completed business-traffic intervals in the selected range. */
export function highWindowCache(row: MonitorMatrixRow, coverage?: MonitorCoverage, range: MonitorRange = '90m') {
  const bounds = coverageBounds(coverage)
  if (!bounds || !coverage?.coverage_complete || !hasMonitorSamples(row.metrics)) return null
  const { bucketSeconds, count } = cacheWindows[range]
  const step = bucketSeconds * 1000
  if (coverage.bucket_seconds !== bucketSeconds || bounds.end - bounds.start !== count * step) return null
  const through = Math.min(bounds.end, Date.parse(coverage.data_through))
  if (!Number.isFinite(through) || !(Date.parse(coverage.coverage_start) <= bounds.start)) return null
  const values: number[] = [], seen = new Set<number>()
  let measured = false
  for (const { bucket_start, metrics } of row.buckets) {
    if (!hasMonitorSamples(metrics)) continue
    const at = Date.parse(bucket_start)
    if (!Number.isFinite(at)) return null
    if (at < bounds.start || at + step > through) continue
    const value = metrics.cache_rate
    if ((at - bounds.start) % step !== 0 || seen.has(at) || !Number.isFinite(value) || value < 0 || value > 1) return null
    seen.add(at)
    values.push(value) // Legacy viewer totals are redacted; keep their reported zeros in the distribution.
    measured ||= value > 0 || metrics.cache_rate_denominator > 0
  }
  if (!measured || values.length < 10) return null
  values.sort((a, b) => a - b)
  return { rate: values[Math.ceil(values.length * 0.9) - 1], count: values.length }
}

// Latest buckets that meet the minimum sample. sample_buckets already ignores
// the selected range. Older payloads only have range buckets, so those stay clipped.
export function monitorCardSampleTimeline(row: MonitorMatrixRow, coverage: MonitorCoverage | undefined): MonitorCardBar[] {
  const dedicated = Array.isArray(row.sample_buckets)
  const source = Array.isArray(row.sample_buckets) ? row.sample_buckets : row.buckets
  const bounds = dedicated ? null : coverageBounds(coverage)
  const widthMs = dedicated ? MONITOR_CARD_SAMPLE_BUCKET_MS : (coverage?.bucket_seconds && coverage.bucket_seconds > 0 ? coverage.bucket_seconds * 1000 : 0)
  return source
    .map(bucket => ({ bucket, at: Date.parse(bucket.bucket_start) }))
    .filter(item => Number.isFinite(item.at) && meetsMonitorSampleMinimum(item.bucket) && (!bounds || (item.at >= bounds.start && item.at < bounds.end)))
    .sort((a, b) => a.at - b.at)
    .slice(-MONITOR_CARD_SLOT_COUNT)
    .map(item => ({
      start: item.at,
      end: widthMs > 0 ? item.at + widthMs : item.at,
      state: item.bucket.health.overall,
      observed: true,
      buckets: [item.bucket],
    }))
}

// Keep real time gaps. Each bar summarizes observed health in one of 18 equal
// time windows, never interpolating a successful request into missing history.
export function monitorCardWindowTimeline(row: MonitorMatrixRow, coverage: MonitorCoverage | undefined, selectedModels: readonly string[] = [], includeIdleProbes = false): MonitorCardBar[] {
  const bounds = coverageBounds(coverage)
  if (!bounds) return []
  const { start, end } = bounds
  const width = (end - start) / MONITOR_CARD_SLOT_COUNT
  const bars: MonitorCardBar[] = Array.from({ length: MONITOR_CARD_SLOT_COUNT }, (_, index) => ({ start: start + index * width, end: start + (index + 1) * width, state: 'unknown', observed: false, buckets: [] }))
  const severity: Record<HealthState, number> = { unknown: 0, healthy: 1, warning: 2, critical: 3 }
  const occupied = new Set<number>()
  const bucketWidth = (coverage?.bucket_seconds || 0) * 1000
  for (const bucket of row.buckets) {
    const at = Date.parse(bucket.bucket_start)
    if (!Number.isFinite(at) || at >= end || !hasMonitorSamples(bucket.metrics)) continue
    // Coarse rollups may overlap more than one display slot. Don't label
    // their overlapping slots as probe-only when the exact request time is unknown.
    const index = Math.floor((at - start) / width)
    const last = Math.min(bars.length, Math.ceil((at + bucketWidth - start) / width))
    for (let i = Math.max(0, index); i < last; i++) occupied.add(i)
    const bar = bars[index]
    if (!bar) continue
    bar.observed = true
    // Keep the original bucket metrics: redacted counts cannot weight rates,
    // and bucket medians cannot be combined into a time-window P50.
    bar.buckets.push(bucket)
    if (severity[bucket.health.overall] > severity[bar.state]) bar.state = bucket.health.overall
  }
  const probeSeverity = { correct: 0, incorrect: 1, error: 2 }
  for (const history of includeIdleProbes ? monitorCandyHistories(row) : []) {
    // A grouped "other" model filter gives no exact probe model to attribute.
    if (selectedModels.length && !selectedModels.includes(history.model)) continue
    for (const result of history.results) {
      const at = Date.parse(result.checked_at)
      if (!Number.isFinite(at) || at < start || at >= end || !Object.prototype.hasOwnProperty.call(probeSeverity, result.verdict)) continue
      const index = Math.floor((at - start) / width)
      const bar = bars[index]
      if (!bar || bar.observed || occupied.has(index)) continue
      const previous = bar.probe?.result
      if (!previous || probeSeverity[result.verdict] > probeSeverity[previous.verdict] ||
          (result.verdict === previous.verdict && at > Date.parse(previous.checked_at))) {
        bar.probe = { model: history.model, result }
      }
    }
  }
  for (const bar of bars) bar.buckets.sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
  return bars
}

export function monitorCardTimeline(row: MonitorMatrixRow, coverage: MonitorCoverage | undefined, mode: MonitorCardTimelineMode = 'windows', selectedModels: readonly string[] = [], v4 = false) {
  if (mode === 'samples') {
    // In V4 only the dedicated five-minute history can populate this view.
    if (v4 && !Array.isArray(row.sample_buckets)) return []
    return monitorCardSampleTimeline(row, coverage)
  }
  return monitorCardWindowTimeline(row, coverage, selectedModels, v4)
}

export function candyDisplayState(history: MonitorCandyHistory, now: number): 'correct' | 'incorrect' | 'error' | 'unknown' | 'stale' {
  const latest = history.results.at(-1)
  if (!latest) return 'unknown'
  const age = now - Date.parse(latest.checked_at)
  if (!Number.isFinite(age) || age > Math.max(180, history.interval_minutes * 60 + 120) * 1000) return 'stale'
  return latest.verdict
}
