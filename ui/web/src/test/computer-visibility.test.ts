import {
  COMPUTER_EVENT_VERSION,
  applyEvent,
  emptyOperatorState,
  type ComputerPhase,
  type EventEnvelope,
  type OperatorState,
} from '@matrixmcl/ion-shared'
import { describe, expect, it } from 'vitest'
import {
  activeComputerTurnID,
  hasActiveComputerActivity,
  hasComputerHistory,
} from '../features/computer/visibility'

const actorID = '11111111-1111-4111-8111-111111111111'
const firstSessionID = '22222222-2222-4222-8222-222222222222'
const secondSessionID = '33333333-3333-4333-8333-333333333333'
const taskID = '44444444-4444-4444-8444-444444444444'
const turnID = '88888888-8888-4888-8888-888888888888'

function computerEvent(
  sequence: number,
  phase: ComputerPhase,
  sessionID = firstSessionID,
  toolID = '55555555-5555-4555-8555-555555555555',
  tool = 'browser_navigate',
): EventEnvelope {
  const terminal = [
    'completed',
    'failed',
    'denied',
    'interrupted',
    'outcome_unknown',
  ].includes(phase)
  return {
    sequence,
    event_id: `66666666-6666-4666-8666-${String(sequence).padStart(12, '0')}`,
    type: phase === 'progress' ? 'tool.delta' : `tool.${phase}`,
    occurred_at: `2026-07-23T12:00:${String(sequence).padStart(2, '0')}.000Z`,
    correlation: {
      actor_id: actorID,
      session_id: sessionID,
      turn_id: turnID,
      task_id: taskID,
      tool_id: toolID,
    },
    payload: {
      protocol_version: COMPUTER_EVENT_VERSION,
      tool_event_id: toolID,
      provider_tool_call_id: `provider-${toolID}`,
      tool,
      operation: tool,
      scope: {
        actor_id: actorID,
        session_id: sessionID,
        turn_id: turnID,
        task_id: taskID,
        agent_id: 'ion',
      },
      risk_class: 'GREEN',
      phase,
      timestamp: `2026-07-23T12:00:${String(sequence).padStart(2, '0')}.000Z`,
      display_kind: 'code',
      source_references: [{ kind: 'tool_event', id: toolID }],
      ...(phase === 'progress' ? { progress: { observed: true } } : {}),
      ...(terminal
        ? {
            terminal_status: phase,
            result: { available: true, bytes: 1 },
          }
        : {}),
    },
  } as EventEnvelope
}

function withEvents(...events: EventEnvelope[]): OperatorState {
  return events.reduce(applyEvent, emptyOperatorState())
}

describe('Computer visibility', () => {
  it('stays hidden for idle chat and retained terminal activity', () => {
    expect(hasActiveComputerActivity(emptyOperatorState(), firstSessionID)).toBe(false)

    const completed = withEvents(
      computerEvent(1, 'started'),
      computerEvent(2, 'completed'),
    )
    expect(hasActiveComputerActivity(completed, firstSessionID)).toBe(false)
    expect(hasComputerHistory(completed, firstSessionID)).toBe(true)
  })

  it.each([
    'requested',
    'awaiting_approval',
    'started',
    'progress',
  ] satisfies ComputerPhase[])('opens for the active %s phase', (phase) => {
    const state = withEvents(computerEvent(1, phase))
    expect(hasActiveComputerActivity(state, firstSessionID)).toBe(true)
    expect(activeComputerTurnID(state, firstSessionID)).toBe(turnID)
  })

  it('keeps ordinary tool calls in history without opening Computer', () => {
    const state = withEvents(
      computerEvent(
        1,
        'started',
        firstSessionID,
        '55555555-5555-4555-8555-555555555555',
        'filesystem_read',
      ),
    )
    expect(hasActiveComputerActivity(state, firstSessionID)).toBe(false)
    expect(hasComputerHistory(state, firstSessionID)).toBe(true)
  })

  it('stays open until every parallel tool is terminal', () => {
    const firstTool = '55555555-5555-4555-8555-555555555555'
    const secondTool = '77777777-7777-4777-8777-777777777777'
    const state = withEvents(
      computerEvent(1, 'started', firstSessionID, firstTool),
      computerEvent(2, 'started', firstSessionID, secondTool),
      computerEvent(3, 'completed', firstSessionID, firstTool),
    )
    expect(hasActiveComputerActivity(state, firstSessionID)).toBe(true)

    const finished = applyEvent(
      state,
      computerEvent(4, 'failed', firstSessionID, secondTool),
    )
    expect(hasActiveComputerActivity(finished, firstSessionID)).toBe(false)
  })

  it('does not expose activity from another conversation', () => {
    const state = withEvents(computerEvent(1, 'started', secondSessionID))
    expect(hasActiveComputerActivity(state, firstSessionID)).toBe(false)
    expect(hasComputerHistory(state, firstSessionID)).toBe(false)
    expect(hasActiveComputerActivity(state, secondSessionID)).toBe(true)
  })

  it('does not let an unsupported lifecycle leave Computer stuck open', () => {
    const unsupported = computerEvent(1, 'started')
    unsupported.payload = { protocol_version: 'future.computer.v9' }
    const state = withEvents(unsupported)
    expect(hasActiveComputerActivity(state, firstSessionID)).toBe(false)
    expect(hasComputerHistory(state, firstSessionID)).toBe(true)
  })
})
