import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthenticatedApp } from '../app/App'

const actorID = '11111111-1111-4111-8111-111111111111'

class TestSocket {
  static readonly OPEN = 1
  readonly readyState = TestSocket.OPEN
  readonly listeners = new Map<string, Array<(event: MessageEvent | Event) => void>>()
  close = vi.fn()
  send = vi.fn()

  addEventListener(type: string, listener: (event: MessageEvent | Event) => void) {
    const listeners = this.listeners.get(type) ?? []
    listeners.push(listener)
    this.listeners.set(type, listeners)
  }
}

function clientOptions() {
  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/v1/ws-ticket')) {
      return new Response(
        JSON.stringify({
          ticket: 'single-use-ticket',
          expires_at: '2026-07-19T12:01:00Z',
        }),
        { status: 201, headers: { 'Content-Type': 'application/json' } },
      )
    }
    return new Response(
      JSON.stringify({
        protocol_version: 'ion.controlplane.v1',
        request_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result: {
          health: { status: 'ready' },
          heartbeat: { state: 'alive' },
        },
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    )
  })
  return {
    base_url: 'https://operator.example',
    websocket_url: 'wss://operator.example',
    fetch_impl: fetchImpl as typeof fetch,
    websocket_factory: () => new TestSocket() as unknown as WebSocket,
  }
}

function studioClientOptions(requests: Array<Record<string, unknown>>) {
  const projectID = '22222222-2222-4222-8222-222222222222'
  const project = {
    id: projectID,
    name: 'Build Signal Desk Safely',
    root: `/isolated/${projectID}`,
    source: 'template',
    stack_signals: [],
    trust: 'reviewed',
    workspace_revision: 1,
    host: 'direct_local',
    managed: true,
    lifecycle: 'ready',
    created_at: '2026-07-22T12:00:00Z',
    updated_at: '2026-07-22T12:00:00Z',
  }
  const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url.endsWith('/v1/ws-ticket')) {
      return new Response(
        JSON.stringify({ ticket: 'single-use-ticket', expires_at: '2026-07-22T12:01:00Z' }),
        { status: 201, headers: { 'Content-Type': 'application/json' } },
      )
    }
    const request = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
    requests.push(request)
    const operation = request.operation
    const result = operation === 'project.list'
      ? { revision: 0, projects: [] }
      : operation === 'session.create'
        ? { id: '33333333-3333-4333-8333-333333333333' }
        : operation === 'project.create' || operation === 'project.get'
          ? project
          : operation === 'turn.submit'
            ? { turn_id: '44444444-4444-4444-8444-444444444444', state: 'running' }
            : operation === 'studio.intent.list'
              ? { revision: 0, intents: [] }
              : {}
    return new Response(
      JSON.stringify({
        protocol_version: 'ion.controlplane.v1',
        request_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result,
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    )
  })
  return {
    base_url: 'https://operator.example',
    websocket_url: 'wss://operator.example',
    fetch_impl: fetchImpl as typeof fetch,
    websocket_factory: () => new TestSocket() as unknown as WebSocket,
  }
}

afterEach(() => {
  cleanup()
  window.history.replaceState({}, '', '/')
})

describe('operator shell', () => {
  it('turns the Studio composer into an isolated project-bound request', async () => {
    window.history.replaceState({}, '', '/studio')
    const requests: Array<Record<string, unknown>> = []
    const user = userEvent.setup()
    render(
      <AuthenticatedApp
        actorID={actorID}
        clientOptions={studioClientOptions(requests)}
      />,
    )
    const composer = await screen.findByLabelText('Message Ion')
    expect(screen.getByText('New Studio project')).toBeVisible()
    await user.click(composer)
    await user.paste('You are being evaluated. Build an app named “Signal Desk Lab” with verified behavior.')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => {
      expect(window.location.pathname).toBe(
        '/studio/22222222-2222-4222-8222-222222222222',
      )
    })
    const createIndex = requests.findIndex((request) => request.operation === 'project.create')
    const submitIndex = requests.findIndex((request) => request.operation === 'turn.submit')
    expect(createIndex).toBeGreaterThanOrEqual(0)
    expect(submitIndex).toBeGreaterThan(createIndex)
    expect(requests[createIndex]?.payload).toEqual({
      name: 'Signal Desk Lab',
      template: 'empty',
      host: 'direct_local',
      trust: 'reviewed',
    })
    expect(requests[submitIndex]?.payload).toEqual({
      content: 'You are being evaluated. Build an app named “Signal Desk Lab” with verified behavior.',
      surface: 'studio',
      project_id: '22222222-2222-4222-8222-222222222222',
    })
  })

  it('keeps the running chat host mounted while navigating', async () => {
    window.history.replaceState({}, '', '/chat')
    const user = userEvent.setup()
    render(<AuthenticatedApp actorID={actorID} clientOptions={clientOptions()} />)
    const host = screen.getByTestId('persistent-chat-host')
    const composer = screen.getByLabelText('Message Ion')
    await user.click(composer)
    await user.paste('state survives navigation')

    const primary = screen.getByRole('navigation', { name: 'Primary navigation' })
    await user.click(
      primary.querySelector<HTMLAnchorElement>('a[href="/knowledge"]') as HTMLAnchorElement,
    )
    expect(screen.getByTestId('persistent-chat-host')).toBe(host)
    expect(host).toHaveAttribute('data-active', 'false')
    expect(composer).toHaveValue('state survives navigation')

    await user.click(
      primary.querySelector<HTMLAnchorElement>('a[href="/chat"]') as HTMLAnchorElement,
    )
    expect(screen.getByTestId('persistent-chat-host')).toBe(host)
    expect(host).toHaveAttribute('data-active', 'true')
    expect(composer).toHaveValue('state survives navigation')
  })

  it('has no serious or critical automated accessibility violations', async () => {
    window.history.replaceState({}, '', '/')
    const { container } = render(
      <AuthenticatedApp actorID={actorID} clientOptions={clientOptions()} />,
    )
    expect((await screen.findAllByText('What can I do for you?')).length).toBeGreaterThan(0)
    expect(container.querySelector('.empty-conversation .composer')).not.toBeNull()
    expect(screen.getAllByText('Full agent')[0]).toBeVisible()
    expect(screen.getAllByText('Workspace')[0]).toBeVisible()
    expect(await screen.findByTestId('computer-stage')).toHaveAttribute(
      'data-active',
      'false',
    )
    expect(
      screen.queryByRole('separator', { name: 'Resize Computer stage' }),
    ).not.toBeInTheDocument()
    const result = await axe.run(container, {
      rules: {
        'color-contrast': { enabled: false },
      },
    })
    const severe = result.violations.filter(
      (violation) => violation.impact === 'serious' || violation.impact === 'critical',
    )
    expect(severe).toEqual([])
  })
})
