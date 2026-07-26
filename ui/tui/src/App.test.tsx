import React from 'react'
import { render } from 'ink-testing-library'
import { describe, expect, it, vi } from 'vitest'
import {
  COMPUTER_EVENT_VERSION,
  DISPLAY_MODEL_VERSION,
  emptyOperatorState,
} from '@matrixmcl/ion-shared'
import { App } from './App.js'

describe('terminal operator shell', () => {
  it('renders browser-parity navigation and status without color', () => {
    process.env.NO_COLOR = '1'
    const transport = {
      actorID: '11111111-1111-4111-8111-111111111111',
      rpc: vi.fn(async () => ({
        protocol_version: 'ion.controlplane.v1',
        request_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result: [],
      })),
    }
    const output = render(
      <App
        connection="ready"
        state={emptyOperatorState()}
        transport={transport as never}
      />,
    )
    expect(output.lastFrame()).toContain('ION')
    expect(output.lastFrame()).toContain('assistant workspace')
    expect(output.lastFrame()).toContain('CHAT')
    expect(output.lastFrame()).toContain('Approvals')
    expect(output.lastFrame()).toContain('Projects')
    expect(output.lastFrame()).toContain('Schedules')
    expect(output.lastFrame()).toContain('Skills')
    expect(output.lastFrame()).toContain('Memory')
    expect(output.lastFrame()).toContain('Computer')
    expect(output.lastFrame()).toContain('Ctrl+E')
    expect(output.lastFrame()).toContain('editor')
    expect(output.lastFrame()).not.toMatch(/[╭╮╰╯│─]/)
    output.unmount()
    delete process.env.NO_COLOR
  })

  it('exposes live server operations as slash suggestions', async () => {
    const transport = {
      actorID: '11111111-1111-4111-8111-111111111111',
      rpc: vi.fn(async () => ({
        protocol_version: 'ion.controlplane.v1',
        request_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result: [{
          operation: 'config.get',
          kind: 'query',
          available: true,
          description: 'Read current settings.',
        }],
      })),
    }
    const output = render(
      <App
        connection="ready"
        state={emptyOperatorState()}
        transport={transport as never}
      />,
    )
    await new Promise((resolve) => setTimeout(resolve, 50))
    output.stdin.write('/config')
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(output.lastFrame()).toContain('/config.get')
    expect(output.lastFrame()).toContain('Read current settings.')
    output.unmount()
  })

  it('shows the shared supervised browser workflow projection in Computer', async () => {
    const sessionID = '22222222-2222-4222-8222-222222222222'
    const transport = {
      actorID: '11111111-1111-4111-8111-111111111111',
      rpc: vi.fn(async (request: { operation: string }) => ({
        protocol_version: 'ion.controlplane.v1',
        request_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result: request.operation === 'browser.workflow.list'
          ? [{
              id: '77777777-7777-4777-8777-777777777777',
              status: 'waiting_for_human',
              origin: 'https://service.test',
              revision: 4,
              handoff: {
                kind: 'passkey',
                consequence: 'Sign in to the requested account',
              },
            }]
          : request.operation === 'session.list'
            ? [{ id: sessionID, title: 'Browser task' }]
            : [],
      })),
    }
    const output = render(
      <App
        connection="ready"
        state={emptyOperatorState()}
        transport={transport as never}
      />,
    )
    output.stdin.write('\u001B')
    await vi.waitFor(() => expect(output.lastFrame()).toContain('Navigation focus'))
    output.stdin.write('9')
    await vi.waitFor(() => expect(output.lastFrame()).toContain('SESSIONS, PROVIDERS'))
    output.stdin.write('r')
    await vi.waitFor(() => expect(output.lastFrame()).toContain('CHAT'))
    output.stdin.write('\u001B')
    await vi.waitFor(() => expect(output.lastFrame()).toContain('Navigation focus'))
    output.stdin.write('8')
    await vi.waitFor(() => {
      expect(output.lastFrame()).toContain('SUPERVISED BROWSER WORKFLOWS')
      expect(output.lastFrame()).toContain('waiting for human · https://service.test · rev 4')
      expect(output.lastFrame()).toContain('passkey: Sign in to')
      expect(output.lastFrame()).toContain('the requested account')
    })
    output.unmount()
  })

  it('shows evidence-backed since-away categories in Tasks', async () => {
    const transport = {
      actorID: '11111111-1111-4111-8111-111111111111',
      rpc: vi.fn(async (request: { operation: string }) => ({
        protocol_version: 'ion.controlplane.v1',
        request_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result: request.operation === 'continuity.brief'
          ? {
              status: 'ready',
              period: '24h',
              sections: [{
                kind: 'changed_files',
                label: 'Changed files',
                items: [{ summary: 'internal/service.go' }],
              }, {
                kind: 'pending_questions',
                label: 'Pending questions',
                items: [{ summary: 'Decision waiting for browser_submit' }],
              }],
            }
          : [],
      })),
    }
    const output = render(
      <App
        connection="ready"
        state={emptyOperatorState()}
        transport={transport as never}
      />,
    )
    output.stdin.write('\u001B')
    await vi.waitFor(() => expect(output.lastFrame()).toContain('Navigation focus'))
    output.stdin.write('2')
    await vi.waitFor(() => {
      expect(output.lastFrame()).toContain('SINCE YOU WERE AWAY · PAST DAY')
      expect(output.lastFrame()).toContain('Changed files: internal/service.go')
      expect(output.lastFrame()).toContain('Pending questions: Decision waiting for browser_submit')
    })
    output.unmount()
  })

  it('shows and runs durable project verification from Projects', async () => {
    const projectID = '22222222-2222-4222-8222-222222222222'
    const manifestID = '33333333-3333-4333-8333-333333333333'
    const runID = '44444444-4444-4444-8444-444444444444'
    const manifest = {
      version: 'ion.project-verification.v1',
      id: manifestID,
      actor_id: '11111111-1111-4111-8111-111111111111',
      project_id: projectID,
      workspace_revision: 1,
      revision: 1,
      criteria: [{ id: 'release.ready', description: 'Release is ready.', kinds: ['test'] }],
      gates: [{
        id: 'test', kind: 'test', argv: ['go', 'test', './...'], timeout_seconds: 30,
        required: true, criteria: ['release.ready'], evidence_kinds: ['logs'], available: true,
      }],
      created_at: '2026-07-25T12:00:00Z',
    }
    const run = {
      version: 'ion.project-verification.v1',
      id: runID,
      actor_id: manifest.actor_id,
      project_id: projectID,
      manifest_id: manifestID,
      manifest_revision: 1,
      workspace_revision: 1,
      mode: 'full',
      status: 'passed',
      results: [],
      criteria_covered: ['release.ready'],
      uncovered_criteria: [],
      repair: { state: 'complete', reason: 'Stable evidence.', attempts: 1, max_attempts: 3, failure_signatures: [] },
      started_at: '2026-07-25T12:00:00Z',
      finished_at: '2026-07-25T12:00:01Z',
    }
    const transport = {
      actorID: manifest.actor_id,
      rpc: vi.fn(async (request: { operation: string }) => ({
        protocol_version: 'ion.controlplane.v1',
        request_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result: request.operation === 'project.list'
          ? { revision: 1, projects: [{ id: projectID, name: 'Verified project', lifecycle: 'ready', root: '/workspace/project' }] }
          : request.operation === 'project.verification.manifest.get'
            ? manifest
            : request.operation === 'project.verification.runs'
              ? []
              : request.operation === 'project.verification.waivers'
                ? []
                : request.operation === 'project.verification.run'
                  ? run
                  : [],
      })),
    }
    const output = render(
      <App connection="ready" state={emptyOperatorState()} transport={transport as never} />,
    )
    output.stdin.write('\u001B')
    await vi.waitFor(() => expect(output.lastFrame()).toContain('Navigation focus'))
    output.stdin.write('4')
    await vi.waitFor(() => {
      expect(output.lastFrame()).toContain('VERIFICATION · not run')
      expect(output.lastFrame()).toContain('1 gates · 0 covered · 1 uncovered')
    })
    output.stdin.write('v')
    await vi.waitFor(() => {
      expect(transport.rpc).toHaveBeenCalledWith(expect.objectContaining({
        operation: 'project.verification.run',
        payload: expect.objectContaining({ project_id: projectID, manifest_id: manifestID, full: true }),
      }))
      expect(output.lastFrame()).toContain('VERIFICATION · passed')
    })
    output.unmount()
  })

  it('queues composer input while the selected conversation is running', async () => {
    const actorID = '11111111-1111-4111-8111-111111111111'
    const sessionID = '22222222-2222-4222-8222-222222222222'
    const turnID = '33333333-3333-4333-8333-333333333333'
    const transport = {
      actorID,
      rpc: vi.fn(async () => ({
        protocol_version: 'ion.controlplane.v1',
        request_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result: [],
      })),
    }
    const state = {
      ...emptyOperatorState(),
      turns: {
        [turnID]: {
          id: turnID,
          session_id: sessionID,
          status: 'running' as const,
          last_sequence: 1,
        },
      },
    }
    const output = render(
      <App connection="ready" state={state} transport={transport as never} />,
    )
    output.stdin.write('continue with the evidence')
    await vi.waitFor(() => {
      expect(output.lastFrame()).toContain('continue with the evidence')
    })
    output.stdin.write('\r')
    await vi.waitFor(() => {
      expect(output.lastFrame()).toContain('1 queued')
    })
    expect(output.lastFrame()).toContain('continue with the evidence')
    expect(transport.rpc).not.toHaveBeenCalledWith(
      expect.objectContaining({ operation: 'turn.submit' }),
    )
    output.unmount()
  })

  it('resumes an encrypted conversation from the terminal system view', async () => {
    const sessionID = '22222222-2222-4222-8222-222222222222'
    const transport = {
      actorID: '11111111-1111-4111-8111-111111111111',
      rpc: vi.fn(async (request: { operation: string }) => ({
        protocol_version: 'ion.controlplane.v1',
        request_id: request.operation === 'session.resume'
          ? 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'
          : 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result: request.operation === 'session.list'
          ? [{ id: sessionID, title: 'Continue the release audit' }]
          : request.operation === 'session.resume'
            ? { session: { id: sessionID }, messages: [] }
            : [],
      })),
    }
    const output = render(
      <App
        connection="ready"
        state={emptyOperatorState()}
        transport={transport as never}
      />,
    )
    await vi.waitFor(() => {
      expect(transport.rpc).toHaveBeenCalledWith(
        expect.objectContaining({ operation: 'session.list' }),
      )
    })
    output.stdin.write('\u001B')
    await vi.waitFor(() => {
      expect(output.lastFrame()).toContain('Navigation focus')
    })
    output.stdin.write('9')
    await vi.waitFor(() => {
      expect(output.lastFrame()).toContain('SESSIONS, PROVIDERS, CHANNELS, SAFETY, AND LOGS')
      expect(output.lastFrame()).toContain('Continue the release audit')
    })
    output.stdin.write('r')
    await vi.waitFor(() => {
      expect(transport.rpc).toHaveBeenCalledWith(expect.objectContaining({
        operation: 'session.resume',
        scope: expect.objectContaining({ session_id: sessionID }),
      }))
      expect(output.lastFrame()).toContain('CHAT')
    })
    output.unmount()
  })

  it('renders validated tool semantics without exposing raw result payloads', async () => {
    const transport = {
      actorID: '11111111-1111-4111-8111-111111111111',
      rpc: vi.fn(async () => ({
        protocol_version: 'ion.controlplane.v1',
        request_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        revision: 0,
        result: [],
      })),
    }
    const actorID = '11111111-1111-4111-8111-111111111111'
    const toolID = '22222222-2222-4222-8222-222222222222'
    const state = {
      ...emptyOperatorState(),
      recent_events: [{
        sequence: 1,
        event_id: '33333333-3333-4333-8333-333333333333',
        type: 'tool.completed' as const,
        occurred_at: '2026-07-23T12:00:00.000Z',
        correlation: { actor_id: actorID, tool_id: toolID },
        payload: {
          protocol_version: COMPUTER_EVENT_VERSION,
          tool_event_id: toolID,
          provider_tool_call_id: 'provider-call',
          tool: 'filesystem_read',
          operation: 'filesystem_read',
          scope: {
            actor_id: actorID,
            outcome_id: toolID,
            agent_id: 'ion',
          },
          risk_class: 'GREEN',
          phase: 'completed',
          timestamp: '2026-07-23T12:00:00.000Z',
          display_kind: 'repository',
          source_references: [{ kind: 'tool_event', id: toolID }],
          terminal_status: 'completed',
          result: { available: true, bytes: 128 },
          display_model: {
            protocol_version: DISPLAY_MODEL_VERSION,
            kind: 'code',
            title: {
              value: 'src/main.go',
              truth: 'observed',
              format: 'path',
              sources: [0],
            },
          },
          raw_secret: 'must-not-render',
        },
      }],
    }
    const output = render(
      <App connection="ready" state={state} transport={transport as never} />,
    )
    output.stdin.write('\u001B')
    await vi.waitFor(() => {
      expect(output.lastFrame()).toContain('Navigation focus')
    })
    output.stdin.write('8')
    await vi.waitFor(() => {
      expect(output.lastFrame()).toContain('COMPUTER')
    })
    expect(output.lastFrame()).toContain('FOLLOWING LIVE')
    expect(output.lastFrame()).toContain('ACTION')
    expect(output.lastFrame()).toContain('STATUS')
    expect(output.lastFrame()).toContain('TRUTH')
    expect(output.lastFrame()).toContain('SOURCE')
    expect(output.lastFrame()).toContain('ARTIFACT')
    expect(output.lastFrame()).toContain('src/main.go')
    expect(output.lastFrame()).not.toContain('must-not-render')
    output.unmount()
  })
})
