import { describe, expect, it } from 'vitest'
import { frameRetryDelay } from '../features/computer/PrivateDesktop'

describe('private desktop frame recovery', () => {
  it('backs off repeated frame failures and caps reconnect delay', () => {
    expect([
      frameRetryDelay(0),
      frameRetryDelay(1),
      frameRetryDelay(2),
      frameRetryDelay(3),
      frameRetryDelay(4),
      frameRetryDelay(5),
      frameRetryDelay(20),
    ]).toEqual([500, 1_000, 2_000, 4_000, 8_000, 10_000, 10_000])
  })
})
