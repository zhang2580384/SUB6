import { describe, expect, it } from 'vitest'
import type { MonitorCoverage, MonitorMatrixBucket, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'
import { highWindowCache } from '../monitorCards'

const at = (min: number) => new Date(Date.UTC(2026, 0, 12) + min * 60000).toISOString()
const coverage = { requested_start: at(0), requested_end: at(90), data_through: at(85), coverage_start: at(-90), bucket_seconds: 300, coverage_complete: true } as MonitorCoverage
const metric = (rate: number, denominator = 0) => ({ has_samples: true, request_count: 0, cache_rate: rate, cache_rate_denominator: denominator }) as MonitorMetric
const bucket = (minute: number, rate: number, denominator = 0) => ({ bucket_start: at(minute), metrics: metric(rate, denominator) }) as MonitorMatrixBucket
const row = (buckets: MonitorMatrixBucket[]) => ({ metrics: metric(0.54), buckets }) as MonitorMatrixRow

describe('V4 high cache in the selected range', () => {
  const ten = [0, 0, 0, 0.02, 0.1, 0.2, 0.3, 0.4, 0.75, 0.99].map((rate, i) => bucket(i * 5, rate))
  it('uses the nearest rank including measured or redacted zeros, without cherry-picking the maximum', () => {
    const r = row([...ten].reverse().concat(bucket(85, 1)))
    r.sample_buckets = [bucket(-5, 1)]
    r.candy_checks = [{ model: 'example', reasoning_effort: 'low', interval_minutes: 5, results: [{ checked_at: at(70), verdict: 'correct', latency_ms: 1, cache_rate: 1 }] }]
    expect(highWindowCache(r, coverage)).toEqual({ rate: 0.75, count: 10 })
  })
  it('leaves small samples, invalid measures, duplicates and mismatched bucket widths on the normal headline', () => {
    expect(highWindowCache(row(ten.slice(1)), coverage)).toBeNull()
    expect(highWindowCache(row(ten.concat(ten[0])), coverage)).toBeNull()
    expect(highWindowCache(row(ten.concat(bucket(70, NaN))), coverage)).toBeNull()
    expect(highWindowCache(row(ten), { ...coverage, bucket_seconds: 3600 })).toBeNull()
    expect(highWindowCache(row(ten), { ...coverage, coverage_complete: false })).toBeNull()
    expect(highWindowCache(row(ten), { ...coverage, data_through: at(44) })).toBeNull()
  })
  it('distinguishes a proven all-zero set from a set with no known input', () => {
    expect(highWindowCache(row(ten.map((_, i) => bucket(i * 5, 0, 1))), coverage)).toEqual({ rate: 0, count: 10 })
    expect(highWindowCache(row(ten.map((_, i) => bucket(i * 5, 0))), coverage)).toBeNull()
  })
})

const ranges = [
  { range: '90m', minutes: 5, slots: 18 },
  { range: '24h', minutes: 60, slots: 24 },
  { range: '7d', minutes: 720, slots: 14 },
  { range: '30d', minutes: 1440, slots: 30 },
] as const

it.each(ranges)('uses completed $range traffic buckets and falls back when fewer than ten qualify', ({ range, minutes, slots }) => {
  const end = minutes * slots
  const bounds = { ...coverage, requested_end: at(end), data_through: at(end - 1), bucket_seconds: minutes * 60 }
  // Leave gaps and include the unfinished last bucket, an old bucket and a bucket without traffic.
  const samples = [0, 0, 0, 0.12, 0.15, 0.2, 0.25, 0.7, 0.82, 1].map((rate, i) => bucket(minutes * (i + 1), rate))
  const empty = bucket(0, 1)
  empty.metrics.has_samples = false
  const measured = row([bucket(-minutes, 1), ...samples, empty, bucket(end - minutes, 1)])
  expect(highWindowCache(measured, bounds, range)).toEqual({ rate: 0.82, count: 10 })
  expect(highWindowCache(measured, { ...bounds, data_through: at(11 * minutes - 1) }, range)).toBeNull()
  expect(highWindowCache(measured, { ...bounds, coverage_start: at(minutes) }, range)).toBeNull()
  expect(highWindowCache(measured, { ...bounds, requested_end: at(end - minutes) }, range)).toBeNull()
})
