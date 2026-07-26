import {
  isComputerEventPayload,
  type ComputerEventPayload,
  type ComputerPhase,
  type EventEnvelope,
  type RecoveryEnvelope,
  type UUID,
} from '../generated/protocol'

export interface TurnState {
  id: UUID
  session_id?: UUID
  status: 'running' | 'recovering' | 'incomplete' | 'completed' | 'failed' | 'cancelled' | 'interrupted'
  last_sequence: number
}

export interface ApprovalState {
  id: UUID
  session_id?: UUID
  turn_id?: UUID
  payload: Record<string, unknown>
  last_sequence: number
}

export interface ComputerActivity {
  id: UUID
  provider_tool_call_id?: string
  tool?: string
  operation?: string
  agent_id?: string
  phase: ComputerPhase | 'unsupported'
  terminal: boolean
  unsupported: boolean
  conflict: boolean
  first_sequence: number
  last_sequence: number
  latest_event_id: UUID
  payload?: ComputerEventPayload
}

export interface OperatorState {
  last_sequence: number
  gap: boolean
  snapshot?: unknown
  recent_events: EventEnvelope[]
  turns: Record<UUID, TurnState>
  pending_approvals: Record<UUID, ApprovalState>
  computer_activities: Record<UUID, ComputerActivity>
  computer_order: UUID[]
}

export function emptyOperatorState(): OperatorState {
  return {
    last_sequence: 0,
    gap: false,
    recent_events: [],
    turns: {},
    pending_approvals: {},
    computer_activities: {},
    computer_order: [],
  }
}

export function applyRecovery(
  previous: OperatorState,
  recovery: RecoveryEnvelope,
): OperatorState {
  let state: OperatorState = recovery.snapshot === undefined
    ? previous
    : {
        ...emptyOperatorState(),
        snapshot: recovery.snapshot,
        gap: recovery.gap_marker !== undefined,
      }
  for (const event of recovery.replay.events) {
    state = applyEvent(state, event)
  }
  state = reconcileActiveTurns(state, recovery.snapshot, recovery.replay.latest_sequence)
  state = reconcilePendingApprovals(state, recovery.snapshot, recovery.replay.latest_sequence)
  return {
    ...state,
    last_sequence: Math.max(state.last_sequence, recovery.replay.latest_sequence),
  }
}

function reconcilePendingApprovals(
	state: OperatorState,
	snapshot: unknown,
	sequence: number,
): OperatorState {
	const pending = asRecord(snapshot)?.pending_approvals
	if (!Array.isArray(pending)) return state

	const approvals: Record<UUID, ApprovalState> = {}
	for (const value of pending) {
		const record = asRecord(value)
		const approvalID = asUUID(record?.id) ?? asUUID(record?.approval_id)
		if (approvalID === undefined) continue
		const sessionID = asUUID(record?.session_id)
		const turnID = asUUID(record?.turn_id)
		approvals[approvalID] = {
			id: approvalID,
			...(sessionID === undefined ? {} : { session_id: sessionID }),
			...(turnID === undefined ? {} : { turn_id: turnID }),
			payload: record ?? {},
			last_sequence: sequence,
		}
	}
	return { ...state, pending_approvals: approvals }
}

export function applyEvent(
  previous: OperatorState,
  event: EventEnvelope,
): OperatorState {
  if (event.sequence <= previous.last_sequence) return previous
  const payload = asRecord(event.payload)
  const next: OperatorState = {
    ...previous,
    last_sequence: event.sequence,
    recent_events: [...previous.recent_events.slice(-999), event],
    turns: { ...previous.turns },
    pending_approvals: { ...previous.pending_approvals },
    computer_activities: { ...previous.computer_activities },
    computer_order: [...previous.computer_order],
  }
  const turnID = event.correlation.turn_id
  if (turnID !== undefined) {
    if (event.type === 'turn.started') {
      next.turns[turnID] = {
        id: turnID,
        ...(event.correlation.session_id === undefined
          ? {}
          : { session_id: event.correlation.session_id }),
        status: 'running',
        last_sequence: event.sequence,
      }
    } else if (event.type === 'turn.recovery' || event.type === 'turn.incomplete' ||
      event.type === 'turn.completed' || event.type === 'turn.failed') {
      const existing = next.turns[turnID]
	  const status = event.type === 'turn.recovery'
	    ? 'recovering'
	    : event.type === 'turn.incomplete'
	      ? payload?.final_honest_partial === false ? 'recovering' : 'incomplete'
	      : event.type === 'turn.completed' ? 'completed' : 'failed'
      next.turns[turnID] = {
        id: turnID,
        ...(existing?.session_id === undefined ? {} : { session_id: existing.session_id }),
        status,
        last_sequence: event.sequence,
      }
    }
  }
  applyComputerEvent(next, event)
  const approvalID = asUUID(payload?.approval_id)
  if (approvalID !== undefined) {
    if (event.type === 'approval.requested' && payload !== undefined) {
      next.pending_approvals[approvalID] = {
        id: approvalID,
        ...(event.correlation.session_id === undefined
          ? {}
          : { session_id: event.correlation.session_id }),
        ...(event.correlation.turn_id === undefined
          ? {}
          : { turn_id: event.correlation.turn_id }),
        payload,
        last_sequence: event.sequence,
      }
    } else if (
      event.type === 'approval.resolved' ||
      event.type === 'approval.expired'
    ) {
      delete next.pending_approvals[approvalID]
    }
  }
  return next
}

const computerEventTypes = new Set([
  'tool.requested',
  'tool.awaiting_approval',
  'tool.started',
  'tool.delta',
  'tool.completed',
  'tool.failed',
  'tool.denied',
  'tool.interrupted',
  'tool.outcome_unknown',
])

function applyComputerEvent(state: OperatorState, event: EventEnvelope): void {
  if (!computerEventTypes.has(event.type)) return
  const toolID = event.correlation.tool_id
  if (toolID === undefined) return
  if (!isComputerEventPayload(event.payload)) {
    const existing = state.computer_activities[toolID]
    if (existing !== undefined) return
    state.computer_activities[toolID] = {
      id: toolID,
      phase: 'unsupported',
      terminal: false,
      unsupported: true,
      conflict: false,
      first_sequence: event.sequence,
      last_sequence: event.sequence,
      latest_event_id: event.event_id,
    }
    state.computer_order = boundedComputerOrder(
      state.computer_order,
      state.computer_activities,
      toolID,
    )
    return
  }
  const payload = event.payload
  if (payload.tool_event_id !== toolID ||
    payload.scope.actor_id !== event.correlation.actor_id) return
  const terminal = isTerminalComputerPhase(payload.phase)
  const existing = state.computer_activities[toolID]
  if (existing?.terminal) {
    if (terminal && existing.phase !== payload.phase) {
      state.computer_activities[toolID] = { ...existing, conflict: true }
    }
    return
  }
  state.computer_activities[toolID] = {
    id: toolID,
    provider_tool_call_id: payload.provider_tool_call_id,
    tool: payload.tool,
    operation: payload.operation,
    agent_id: payload.scope.agent_id,
    phase: payload.phase,
    terminal,
    unsupported: false,
    conflict: existing?.conflict ?? false,
    first_sequence: existing?.first_sequence ?? event.sequence,
    last_sequence: event.sequence,
    latest_event_id: event.event_id,
    payload,
  }
  state.computer_order = boundedComputerOrder(
    state.computer_order,
    state.computer_activities,
    toolID,
  )
}

function isTerminalComputerPhase(phase: ComputerPhase): boolean {
  return phase === 'completed' || phase === 'failed' || phase === 'denied' ||
    phase === 'interrupted' || phase === 'outcome_unknown'
}

function boundedComputerOrder(
  previous: UUID[],
  activities: Record<UUID, ComputerActivity>,
  toolID: UUID,
): UUID[] {
  const order = previous.includes(toolID) ? previous : [...previous, toolID]
  if (order.length <= 1000) return order
  const removable = order.find((id) => activities[id]?.terminal === true)
  if (removable === undefined) return order.slice(-1000)
  delete activities[removable]
  return order.filter((id) => id !== removable)
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return undefined
  }
  return value as Record<string, unknown>
}

function asUUID(value: unknown): UUID | undefined {
  return typeof value === 'string' ? value : undefined
}

function reconcileActiveTurns(
  state: OperatorState,
  snapshot: unknown,
  sequence: number,
): OperatorState {
  const active = asRecord(snapshot)?.active_turns
  if (!Array.isArray(active)) return state

  const turns = { ...state.turns }
  const authoritative = new Set<UUID>()
  for (const value of active) {
    const record = asRecord(value)
    const turnID = asUUID(record?.turn_id)
    if (turnID === undefined) continue
    authoritative.add(turnID)
    const sessionID = asUUID(record?.session_id)
	const rawStatus = typeof record?.status === 'string' ? record.status : 'running'
	const status: TurnState['status'] = rawStatus === 'recovering' || rawStatus === 'incomplete' ||
	  rawStatus === 'completed' || rawStatus === 'failed' || rawStatus === 'cancelled' ||
	  rawStatus === 'interrupted' ? rawStatus : 'running'
    turns[turnID] = {
      id: turnID,
      ...(sessionID === undefined ? {} : { session_id: sessionID }),
	  status,
      last_sequence: sequence,
    }
  }
  for (const [turnID, turn] of Object.entries(turns)) {
	if ((turn.status === 'running' || turn.status === 'recovering') && !authoritative.has(turnID)) {
	  delete turns[turnID]
	}
  }
  return { ...state, turns }
}
