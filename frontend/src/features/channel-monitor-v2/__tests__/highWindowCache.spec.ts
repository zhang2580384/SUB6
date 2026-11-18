import { describe, expect, it } from 'vitest'
import type { MonitorCoverage, MonitorMatrixBucket, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'
import { highWindowCache } from '../monitorCards'

const at = (min: number) => new Date(Date.UTC(2026, 0, 12) + min * 60000).toISOString()
const coverage = { requested_start: at(0), requested_end: at(90), data_through: at(85), coverage_start: at(-90), bucket_seconds: 300, coverage_complete: true } as MonitorCoverage
const metric = (rate: number, denominator = 0) => ({ has_samples: true, request_count: 0, cache_rate: rate, cache_rate_denominator: denominator }) as MonitorMetric
const bucket = (minute: number, rate: number, denominator = 0) => ({ bucket_start: at(minute), metrics: metric(rate, denominator) }) as MonitorMatrixBucket
const row = (buckets: MonitorMatrixBucket[]) => ({ metrics: metric(0.54), buckets }) as MonitorMatrixRow

describe('V4 high cache in the original range', () => {
  const ten = [0, 0, 0, 0.02, 0.1, 0.2, 0.3, 0.4, 0.75, 0.99].map((rate, i) => bucket(i * 5, rate))
  it('uses the nearest rank including measured or redacted zeros, without cherry-picking the maximum', () => {
    const r = row([...ten].reverse().concat(bucket(85, 1)))
    r.sample_buckets = [bucket(-5, 1)]
    r.candy_checks = [{ model: 'example', reasoning_effort: 'low', interval_minutes: 5, results: [{ checked_at: at(70), verdict: 'correct', latency_ms: 1, cache_rate: 1 }] }]
    expect(highWindowCache(r, coverage)).toEqual({ rate: 0.75, count: 10 })
  })
  it('leaves small samples, invalid measures, duplicates and other ranges on the normal headline', () => {
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
