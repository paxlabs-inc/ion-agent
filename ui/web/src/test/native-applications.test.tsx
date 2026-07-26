import { cleanup, render } from '@testing-library/react'
import {
  DISPLAY_MODEL_VERSION,
  type DisplayBlock,
  type DisplayKind,
  type DisplayModel,
  type EventEnvelope,
} from '@matrixmcl/ion-shared'
import { afterEach, describe, expect, it } from 'vitest'
import { NativeApplication } from '../features/computer/NativeApplications'

const source = { kind: 'tool_event', id: '22222222-2222-4222-8222-222222222222' }
const event: EventEnvelope = {
  sequence: 7,
  event_id: '11111111-1111-4111-8111-111111111111',
  type: 'tool.completed',
  occurred_at: '2026-07-23T12:00:00.000Z',
  correlation: { actor_id: '33333333-3333-4333-8333-333333333333' },
  payload: {},
}

function datum(
  value: string,
  format: 'text' | 'url' | 'path' | 'terminal' = 'text',
) {
  return { value, truth: 'observed' as const, format, sources: [0] }
}

function model(kind: DisplayKind, blocks?: DisplayBlock[]): DisplayModel {
  return {
    protocol_version: DISPLAY_MODEL_VERSION,
    kind,
    title: datum(`${kind} view`),
    fields: [
      { label: 'Status', value: datum('ready') },
      { label: 'URL', value: datum('https://example.com/research', 'url') },
      { label: 'Path', value: datum('src/main.go', 'path') },
    ],
    ...(blocks === undefined ? {} : { blocks }),
  }
}

afterEach(cleanup)

describe('native Computer application registry', () => {
  it.each([
    ['search', 'research'],
    ['reader', 'research'],
    ['navigation', 'workspace'],
    ['repository', 'workspace'],
    ['code', 'workspace'],
    ['diff', 'workspace'],
    ['terminal', 'terminal'],
    ['process', 'terminal'],
    ['table', 'deliverable'],
    ['chart', 'deliverable'],
    ['document', 'deliverable'],
    ['artifact', 'deliverable'],
    ['task', 'task'],
    ['agent', 'task'],
    ['approval', 'task'],
    ['error', 'generic'],
    ['degraded', 'generic'],
  ] as const)('registers %s with the %s renderer', (kind, renderer) => {
    const { container } = render(
      <NativeApplication
        display={model(kind)}
        event={event}
        migrated={false}
        sources={[source]}
      />,
    )
    expect(container.querySelector('.computer-native-app')).toHaveAttribute(
      'data-renderer',
      renderer,
    )
    expect(container.textContent).toContain('Observed · Source 1')
  })

  it('renders research links, results, and citations as inert semantic content', () => {
    const search = model('search', [{
      kind: 'list',
      label: 'Results',
      items: [{
        fields: [
          { label: 'Title', value: datum('Primary source') },
          { label: 'URL', value: datum('https://example.com/source', 'url') },
          { label: 'Snippet', value: datum('A bounded observed excerpt.') },
        ],
      }],
    }])
    const { container, getByRole, getByText } = render(
      <NativeApplication
        display={search}
        event={event}
        migrated={false}
        sources={[source]}
      />,
    )
    expect(getByRole('link', { name: 'Primary source' })).toHaveAttribute(
      'rel',
      'noreferrer noopener nofollow',
    )
    expect(getByText('A bounded observed excerpt.')).toBeVisible()
    expect(container.querySelector('iframe, script, style')).toBeNull()
  })

  it('renders terminal output and data through accessible bounded primitives', () => {
    const terminal = model('terminal', [{
      kind: 'terminal',
      content: datum('build complete', 'terminal'),
    }])
    const terminalView = render(
      <NativeApplication
        display={terminal}
        event={event}
        migrated={false}
        sources={[source]}
      />,
    )
    expect(terminalView.getByRole('log', { name: 'Terminal output' })).toHaveTextContent(
      'build complete',
    )
    terminalView.unmount()

    const table = model('table', [{
      kind: 'table',
      label: 'Build results',
      columns: ['Target', 'Status'],
      rows: [[datum('web'), datum('passed')]],
    }])
    const dataView = render(
      <NativeApplication
        display={table}
        event={event}
        migrated={false}
        sources={[source]}
      />,
    )
    expect(dataView.getByRole('table', { name: 'Build results' })).toBeVisible()
    expect(dataView.getByRole('columnheader', { name: 'Target' })).toBeVisible()
    expect(dataView.getByRole('cell', { name: /passed/i })).toBeVisible()
  })

  it('labels visual captures as secondary evidence instead of a live interface', () => {
    const { getByRole, getByText } = render(
      <NativeApplication
        display={model('reader')}
        event={event}
        migrated={false}
        sources={[
          source,
          { kind: 'screenshot', id: 'captured-evidence-7' },
        ]}
      />,
    )
    expect(getByRole('heading', { name: 'Captured visual evidence' })).toBeVisible()
    expect(getByText(/Semantic data remains the primary view/)).toBeVisible()
  })
})
