import {
  COMPUTER_EVENT_VERSION,
  DISPLAY_MODEL_VERSION,
  isEventEnvelope,
  OperatorEventStore,
  PROTOCOL_VERSION,
  type EventEnvelope,
  type RecoveryEnvelope,
} from '@matrixmcl/ion-shared'
import { describe, expect, it, vi } from 'vitest'

const actorID = '11111111-1111-4111-8111-111111111111'
const sessionID = '22222222-2222-4222-8222-222222222222'
const turnID = '33333333-3333-4333-8333-333333333333'
const approvalID = '44444444-4444-4444-8444-444444444444'
const toolA = '55555555-5555-4555-8555-555555555555'
const toolB = '66666666-6666-4666-8666-666666666666'

function event(
  sequence: number,
  type: EventEnvelope['type'],
  payload: unknown,
): EventEnvelope {
  return {
    sequence,
    event_id: `aaaaaaaa-aaaa-4aaa-8aaa-${String(sequence).padStart(12, '0')}`,
    type,
    occurred_at: '2026-07-19T12:00:00.000Z',
    correlation: {
      actor_id: actorID,
      session_id: sessionID,
      turn_id: turnID,
    },
    payload,
  }
}

function computerEvent(
  sequence: number,
  toolID: string,
  phase: 'requested' | 'awaiting_approval' | 'started' | 'progress' |
    'completed' | 'failed' | 'denied' | 'interrupted' | 'outcome_unknown',
): EventEnvelope {
  const eventType = phase === 'progress' ? 'tool.delta' : `tool.${phase}`
  const terminal = ['completed', 'failed', 'denied', 'interrupted', 'outcome_unknown']
    .includes(phase)
  const value = event(sequence, eventType as EventEnvelope['type'], {
    protocol_version: COMPUTER_EVENT_VERSION,
    tool_event_id: toolID,
    provider_tool_call_id: `provider-${toolID}`,
    tool: 'filesystem_read',
    operation: 'filesystem_read',
    scope: {
      actor_id: actorID,
      session_id: sessionID,
      turn_id: turnID,
      outcome_id: turnID,
      agent_id: toolID === toolA ? 'ion' : 'research-agent',
    },
    risk_class: 'GREEN',
    phase,
    timestamp: '2026-07-19T12:00:00.000Z',
    display_kind: 'repository',
    source_references: [{ kind: 'tool_event', id: toolID }],
    ...(phase === 'progress' ? { progress: { bytes: 64 } } : {}),
    ...(terminal ? {
      terminal_status: phase,
      result: { available: phase === 'completed', bytes: phase === 'completed' ? 64 : 0 },
      display_model: {
        protocol_version: DISPLAY_MODEL_VERSION,
        kind: phase === 'completed' ? 'code' : 'error',
        title: {
          value: phase === 'completed' ? 'main.go' : 'Action failed',
          truth: phase === 'completed' ? 'observed' : 'summarized',
          format: phase === 'completed' ? 'path' : 'text',
          sources: [0],
        },
      },
    } : {}),
  })
  value.correlation.tool_id = toolID
  return value
}

describe('OperatorEventStore', () => {
  it('reconstructs turns and approvals and ignores duplicate delivery', () => {
    expect(PROTOCOL_VERSION).toBe('ion.controlplane.v1')
    const store = new OperatorEventStore()
    const listener = vi.fn()
    store.subscribe(listener)
    const recovery: RecoveryEnvelope = {
      replay: {
        after_sequence: 0,
        earliest_sequence: 1,
        latest_sequence: 2,
        head_sequence: 2,
        gap: false,
        events: [
          event(1, 'turn.started', { state: 'running' }),
          event(2, 'approval.requested', {
            approval_id: approvalID,
            operation: 'Release production',
          }),
        ],
      },
      snapshot: { source: 'durable' },
    }
    store.recover(recovery)
    expect(store.getSnapshot().turns[turnID]?.status).toBe('running')
    expect(store.getSnapshot().pending_approvals[approvalID]?.payload.operation).toBe(
      'Release production',
    )
    store.accept(event(2, 'approval.requested', { approval_id: approvalID }))
    expect(listener).toHaveBeenCalledTimes(1)

    store.accept(event(3, 'approval.resolved', { approval_id: approvalID }))
    store.accept(event(4, 'turn.completed', { state: 'completed' }))
    expect(store.getSnapshot().pending_approvals[approvalID]).toBeUndefined()
    expect(store.getSnapshot().turns[turnID]?.status).toBe('completed')
    expect(store.getSnapshot().last_sequence).toBe(4)
  })

  it('marks explicit gap recovery and stays memory bounded', () => {
    const store = new OperatorEventStore()
    store.recover({
      replay: {
        after_sequence: 10,
        earliest_sequence: 50,
        latest_sequence: 50,
        head_sequence: 50,
        gap: true,
        events: [],
      },
      gap_marker: {
        requested_after: 10,
        available_from: 50,
        latest: 50,
      },
      snapshot: { fresh: true },
    })
    for (let sequence = 51; sequence <= 1_151; sequence += 1) {
      store.accept(event(sequence, 'heartbeat.pulse', {}))
    }
    expect(store.getSnapshot().gap).toBe(true)
    expect(store.getSnapshot().recent_events).toHaveLength(1_000)
    expect(store.getSnapshot().last_sequence).toBe(1_151)
  })

  it('reconciles a truncated replay with authoritative active turns', () => {
    const store = new OperatorEventStore()
    store.recover({
      replay: {
        after_sequence: 0,
        earliest_sequence: 1,
        latest_sequence: 3_000,
        head_sequence: 3_000,
        gap: false,
        events: [event(1_900, 'turn.started', { state: 'running' })],
      },
      snapshot: { active_turns: [] },
    })
    expect(store.getSnapshot().turns[turnID]).toBeUndefined()
    expect(store.getSnapshot().last_sequence).toBe(3_000)

    store.recover({
      replay: {
        after_sequence: 3_000,
        earliest_sequence: 1,
        latest_sequence: 3_100,
        head_sequence: 3_100,
        gap: false,
        events: [],
      },
      snapshot: {
        active_turns: [{ turn_id: turnID, session_id: sessionID, status: 'recovering' }],
      },
    })
    expect(store.getSnapshot().turns[turnID]).toEqual({
      id: turnID,
      session_id: sessionID,
	  status: 'recovering',
      last_sequence: 3_100,
    })
  })

	it('does not project a final incomplete turn as phantom running work', () => {
	  const store = new OperatorEventStore()
	  store.accept(event(1, 'turn.started', { state: 'running' }))
	  store.accept(event(2, 'turn.recovery', { state: 'recovering', action: 'respawn' }))
	  expect(store.getSnapshot().turns[turnID]?.status).toBe('recovering')
	  store.accept(event(3, 'turn.incomplete', {
		state: 'incomplete',
		phase: 'provider_tool_markup',
		final_honest_partial: true,
	  }))
	  expect(store.getSnapshot().turns[turnID]?.status).toBe('incomplete')

	  store.recover({
		replay: {
		  after_sequence: 3,
		  earliest_sequence: 1,
		  latest_sequence: 4,
		  head_sequence: 4,
		  gap: false,
		  events: [],
		},
		snapshot: {
		  active_turns: [{
			turn_id: turnID,
			session_id: sessionID,
			status: 'incomplete',
		  }],
		},
	  })
	  expect(store.getSnapshot().turns[turnID]?.status).toBe('incomplete')
	})

	it('keeps a recoverable incomplete checkpoint in recovery until completion', () => {
	  const store = new OperatorEventStore()
	  store.accept(event(1, 'turn.started', { state: 'running' }))
	  store.accept(event(2, 'turn.incomplete', {
		state: 'incomplete',
		phase: 'answer_validation',
		final_honest_partial: false,
	  }))
	  expect(store.getSnapshot().turns[turnID]?.status).toBe('recovering')
	  store.accept(event(3, 'turn.recovery', {
		state: 'recovering',
		action: 'respawn',
		phase: 'answer_validation',
	  }))
	  store.accept(event(4, 'turn.completed', { state: 'completed' }))
	  expect(store.getSnapshot().turns[turnID]?.status).toBe('completed')
	})

  it('reconciles pending approvals from the authoritative snapshot', () => {
	const staleApproval = '55555555-5555-4555-8555-555555555555'
	const store = new OperatorEventStore()
	store.accept(event(1, 'approval.requested', {
		approval_id: staleApproval,
		operation: 'Stale operation',
	}))
	store.recover({
		replay: {
			after_sequence: 1,
			earliest_sequence: 1,
			latest_sequence: 3_000,
			head_sequence: 3_000,
			gap: false,
			events: [],
		},
		snapshot: {
			pending_approvals: [{
				id: approvalID,
				session_id: sessionID,
				turn_id: turnID,
				operation: 'Release production',
				consequence: 'Publishes the verified release',
			}],
		},
	})
	const snapshot = store.getSnapshot()
	expect(snapshot.pending_approvals[staleApproval]).toBeUndefined()
	expect(snapshot.pending_approvals[approvalID]).toMatchObject({
		id: approvalID,
		session_id: sessionID,
		turn_id: turnID,
		last_sequence: 3_000,
	})
	expect(snapshot.pending_approvals[approvalID]?.payload.operation).toBe(
		'Release production',
	)
  })

  it('reconstructs parallel computer lifecycles with one terminal truth', () => {
    const store = new OperatorEventStore()
    const replayEvents = [
      computerEvent(1, toolA, 'requested'),
      computerEvent(2, toolB, 'requested'),
      computerEvent(3, toolA, 'started'),
      computerEvent(4, toolB, 'started'),
      computerEvent(5, toolA, 'progress'),
      computerEvent(6, toolB, 'failed'),
      computerEvent(7, toolA, 'completed'),
    ]
    store.recover({
      replay: {
        after_sequence: 0,
        earliest_sequence: 1,
        latest_sequence: 7,
        head_sequence: 7,
        gap: false,
        events: replayEvents,
      },
      snapshot: { source: 'durable' },
    })
    const snapshot = store.getSnapshot()
    expect(snapshot.computer_order).toEqual([toolA, toolB])
    expect(snapshot.computer_activities[toolA]).toMatchObject({
      id: toolA,
      phase: 'completed',
      terminal: true,
      unsupported: false,
      conflict: false,
      first_sequence: 1,
      last_sequence: 7,
      agent_id: 'ion',
    })
    expect(snapshot.computer_activities[toolB]).toMatchObject({
      phase: 'failed',
      terminal: true,
      agent_id: 'research-agent',
    })

    store.accept(computerEvent(7, toolA, 'completed'))
    expect(store.getSnapshot()).toBe(snapshot)
    store.accept(computerEvent(8, toolA, 'failed'))
    expect(store.getSnapshot().computer_activities[toolA]).toMatchObject({
      phase: 'completed',
      terminal: true,
      conflict: true,
    })
  })

  it('keeps retained legacy tool events explicit instead of reinterpreting them', () => {
    const store = new OperatorEventStore()
    const legacy = event(1, 'tool.completed', {
      name: 'filesystem_read',
      failed: false,
    })
    legacy.correlation.tool_id = toolA
    store.accept(legacy)
    expect(store.getSnapshot().computer_activities[toolA]).toMatchObject({
      phase: 'unsupported',
      unsupported: true,
      terminal: false,
    })
  })

  it('rejects malformed current computer payloads but accepts explicit legacy history', () => {
    const valid = computerEvent(1, toolA, 'completed')
    expect(isEventEnvelope(valid)).toBe(true)
    const mismatched = structuredClone(valid)
    mismatched.type = 'tool.failed'
    expect(isEventEnvelope(mismatched)).toBe(false)
    const unsafeDisplay = structuredClone(valid)
    const unsafePayload = unsafeDisplay.payload as Record<string, unknown>
    unsafePayload.display_model = {
      protocol_version: DISPLAY_MODEL_VERSION,
      kind: 'code',
      title: {
        value: '<script>unsafe</script>',
        truth: 'observed',
        format: 'text',
        sources: [0],
      },
    }
    expect(isEventEnvelope(unsafeDisplay)).toBe(false)
    const futureDisplay = structuredClone(valid)
    const futurePayload = futureDisplay.payload as Record<string, unknown>
    futurePayload.display_model = {
      protocol_version: 'ion.display-model.v99',
      opaque: { retained: true },
    }
    expect(isEventEnvelope(futureDisplay)).toBe(true)
    const legacy = event(2, 'tool.completed', { name: 'filesystem_read' })
    legacy.correlation.tool_id = toolA
    expect(isEventEnvelope(legacy)).toBe(true)
  })
})
