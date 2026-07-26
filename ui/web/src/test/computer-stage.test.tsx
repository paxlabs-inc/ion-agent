import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  COMPUTER_EVENT_VERSION,
  DISPLAY_MODEL_VERSION,
  emptyOperatorState,
  type EventEnvelope,
  type OperatorState,
} from '@matrixmcl/ion-shared'
import { afterEach, describe, expect, it, vi } from 'vitest'

const context = vi.hoisted(() => ({
  connection: 'ready' as 'connecting' | 'ready' | 'degraded',
  command: vi.fn(),
  control: undefined as Record<string, unknown> | undefined,
  workflows: undefined as unknown[] | undefined,
  sessionID: undefined as string | undefined,
  state: undefined as OperatorState | undefined,
}))

vi.mock('../app/operator-context', () => ({
  useOperator: () => ({
    client: { query: vi.fn() },
    command: context.command,
    connection: context.connection,
    sessionID: context.sessionID,
  }),
  useOperatorState: () => context.state ?? emptyOperatorState(),
}))

vi.mock('@tanstack/react-query', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@tanstack/react-query')>()),
  useQuery: (options: { queryKey?: unknown[] }) => ({
    data: options.queryKey?.[0] === 'computer-control'
      ? context.control
      : options.queryKey?.[0] === 'browser-workflows'
        ? context.workflows
      : undefined,
    refetch: vi.fn(),
  }),
}))

import { ComputerStage } from '../features/computer/ComputerStage'

const actorID = '11111111-1111-4111-8111-111111111111'
const toolID = '22222222-2222-4222-8222-222222222222'
const taskID = '33333333-3333-4333-8333-333333333333'

function computerEvent(
  sequence: number,
  phase: 'started' | 'progress' | 'completed',
  title: string,
  agentID = 'ion',
): EventEnvelope {
  const eventID = `44444444-4444-4444-8444-${String(sequence).padStart(12, '0')}`
  return {
    sequence,
    event_id: eventID,
    type: phase === 'progress' ? 'tool.delta' : `tool.${phase}`,
    occurred_at: `2026-07-23T12:00:0${String(sequence)}.000Z`,
    correlation: { actor_id: actorID, task_id: taskID, tool_id: toolID },
    payload: {
      protocol_version: COMPUTER_EVENT_VERSION,
      tool_event_id: toolID,
      provider_tool_call_id: 'provider-call',
      tool: 'repository_read',
      operation: 'repository_read',
      scope: { actor_id: actorID, task_id: taskID, agent_id: agentID },
      risk_class: 'GREEN',
      phase,
      timestamp: `2026-07-23T12:00:0${String(sequence)}.000Z`,
      display_kind: 'repository',
      source_references: [{ kind: 'tool_event', id: toolID }],
      ...(phase === 'progress' ? { progress: { observed: true } } : {}),
      ...(phase === 'completed'
        ? {
            terminal_status: 'completed',
            result: { available: true, bytes: 128 },
          }
        : {}),
      display_model: {
        protocol_version: DISPLAY_MODEL_VERSION,
        kind: 'repository',
        title: {
          value: title,
          truth: 'observed',
          format: 'path',
          sources: [0],
        },
      },
    },
  }
}

afterEach(() => {
  cleanup()
  context.connection = 'ready'
  context.control = undefined
  context.workflows = undefined
  context.command.mockReset()
  context.sessionID = undefined
  context.state = undefined
})

describe('Computer stage', () => {
  it('follows live activity, pauses on history, and returns to live without mutating work', async () => {
    const user = userEvent.setup()
    const first = computerEvent(1, 'started', 'src/first.go')
    const second = computerEvent(2, 'progress', 'src/current.go')
    context.state = {
      ...emptyOperatorState(),
      last_sequence: 2,
      recent_events: [first, second],
    }
    const rendered = render(<ComputerStage />)

    expect(screen.getAllByText('src/current.go')).toHaveLength(2)
    expect(screen.getByText('Following live activity')).toBeVisible()
    await user.click(screen.getAllByRole('button', { name: /Repository Read/i })[0]!)
    expect(screen.getAllByText('src/first.go')).toHaveLength(2)
    expect(screen.getByText('Viewing history while work continues')).toBeVisible()

    const third = computerEvent(3, 'completed', 'src/finished.go')
    context.state = {
      ...context.state,
      last_sequence: 3,
      recent_events: [first, second, third],
    }
    rendered.rerender(<ComputerStage />)
    expect(screen.getAllByText('src/first.go')).toHaveLength(2)
    expect(screen.queryByText('src/finished.go')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Back to live' }))
    expect(screen.getAllByText('src/finished.go')).toHaveLength(2)
  })

  it('supports read-only exploration and a keyboard-dismissable full-screen view', async () => {
    const user = userEvent.setup()
    context.state = {
      ...emptyOperatorState(),
      last_sequence: 1,
      recent_events: [computerEvent(1, 'started', 'src/main.go')],
    }
    render(<ComputerStage />)
    const stage = screen.getByTestId('computer-stage')

    await user.click(screen.getByRole('button', { name: 'Explore' }))
    expect(screen.getByRole('complementary', { name: 'Activity facts and sources' })).toBeVisible()
    expect(screen.getByText('Exploring is read-only. Use explicit conversation controls to steer, retry, approve, or take over.')).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Open Computer full screen' }))
    expect(stage).toHaveAttribute('data-fullscreen', 'true')
    await user.keyboard('{Escape}')
    expect(stage).toHaveAttribute('data-fullscreen', 'false')
  })

  it('preserves independent live positions while switching agent workspaces', async () => {
    const user = userEvent.setup()
    context.state = {
      ...emptyOperatorState(),
      last_sequence: 2,
      recent_events: [
        computerEvent(1, 'started', 'Research evidence', 'researcher'),
        computerEvent(2, 'started', 'Review evidence', 'reviewer'),
      ],
    }
    render(<ComputerStage />)

    await user.click(screen.getByRole('button', { name: /Researcher/ }))
    expect(screen.getAllByText('Research evidence')).toHaveLength(2)
    await user.click(screen.getByRole('button', { name: 'Pause view' }))
    expect(screen.getByText('Viewing history while work continues')).toBeVisible()

    await user.click(screen.getByRole('button', { name: /Reviewer/ }))
    expect(screen.getByText('Following live activity')).toBeVisible()
    expect(screen.getAllByText('Review evidence')).toHaveLength(2)

    await user.click(screen.getByRole('button', { name: /Researcher/ }))
    expect(screen.getByText('Viewing history while work continues')).toBeVisible()
    expect(screen.getByText(/Other agents continue in the background/)).toBeVisible()
  })

  it('reports recovery, retention, and unsupported lifecycle states honestly', () => {
    context.connection = 'connecting'
    const { rerender } = render(<ComputerStage />)
    expect(screen.getByText('Connecting to Computer')).toBeVisible()

    context.connection = 'ready'
    context.state = {
      ...emptyOperatorState(),
      gap: true,
      last_sequence: 1,
      recent_events: [{
        ...computerEvent(1, 'started', 'not used'),
        payload: { protocol_version: 'future.computer.v9' },
      }],
    }
    rerender(<ComputerStage />)
    expect(screen.getByText('This retained activity cannot be displayed')).toBeVisible()
    expect(screen.getByText(/Earlier activity is outside the retained event window/)).toBeVisible()
  })

  it('shows real browser lease authority, expiry, and operator controls', () => {
    const sessionID = '55555555-5555-4555-8555-555555555555'
    const event = computerEvent(1, 'started', 'Controlled browser')
    event.correlation.session_id = sessionID
    if (typeof event.payload === 'object' && event.payload !== null) {
      Object.assign(event.payload, {
        tool: 'browser_navigate',
        operation: 'browser_navigate',
        display_kind: 'navigation',
      })
      const payload = event.payload as { scope: Record<string, unknown> }
      payload.scope.session_id = sessionID
    }
    context.sessionID = sessionID
    context.state = {
      ...emptyOperatorState(),
      last_sequence: 1,
      recent_events: [event],
    }
    context.control = {
      protocol_version: 'ion.computer-control-lease.v1',
      lease_id: '66666666-6666-4666-8666-666666666666',
      target: {
        actor_id: actorID,
        session_id: sessionID,
        resource_kind: 'browser',
        resource_id: sessionID,
      },
      owner: {
        task_id: taskID,
        agent_id: 'ion',
        action: 'browser_navigate',
        revision: 1,
      },
      state: 'active',
      authority: 'operator',
      revision: 1,
      expires_at: '2026-07-24T12:01:30.000Z',
      reconciliation: 'executor_paused_at_action_boundary',
    }
    render(<ComputerStage />)

    expect(screen.getByText('You have control')).toBeVisible()
    expect(screen.getByText('Executor Paused At Action Boundary')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Renew control' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Return control' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Refresh page state' })).toBeVisible()
    expect(screen.getByLabelText('Address')).toBeVisible()
    expect(screen.getByLabelText('Element reference')).toBeVisible()
  })

  it('shows supervised workflow status and routes lifecycle controls through the shared protocol', async () => {
    const user = userEvent.setup()
    const sessionID = '55555555-5555-4555-8555-555555555555'
    const workflowID = '77777777-7777-4777-8777-777777777777'
    context.sessionID = sessionID
    context.command.mockResolvedValue({ result: { status: 'paused' } })
    context.workflows = [{
      id: workflowID,
      status: 'active',
      origin: 'https://service.test',
      revision: 3,
      preview: {
        url: 'https://service.test/',
        title: 'Service sign in',
        text: 'Sign in',
        elements: [],
      },
      updated_at: '2026-07-25T12:00:00Z',
    }]
    render(<ComputerStage />)

    expect(screen.getByText('Service sign in')).toBeVisible()
    expect(screen.getByText('Active · revision 3')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Pause' }))
    expect(context.command).toHaveBeenCalledWith(
      'browser.workflow.pause',
      { workflow_id: workflowID },
      expect.any(String),
      { session_id: sessionID },
    )
    await user.click(screen.getByText('Store an origin-bound credential reference'))
    expect(screen.getByText(/write-only and is never returned/)).toBeVisible()
  })
})
