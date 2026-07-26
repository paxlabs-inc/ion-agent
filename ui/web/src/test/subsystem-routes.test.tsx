import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { StructuredValue, surfaceState } from '../routes/SubsystemRoutes'

describe('subsystem pages', () => {
  it('treats embedded unavailable projections as errors', () => {
    expect(
      surfaceState(false, false, {
        status: 'unavailable',
        reason: 'internal detail',
      }),
    ).toBe('error')
    expect(
      surfaceState(false, false, {
        status: 'not_available',
        reason: 'no records yet',
      }),
    ).toBe('empty')
  })

  it('renders assumptions as readable records instead of JSON', () => {
    const affectedID = 'd069df66-6309-439a-b829-a919831103b3'
    const { container } = render(
      <StructuredValue
        operation="premise.list"
        value={[
          {
            id: '2170e542-538c-47b4-86ca-fae3895a171b',
            affected_subgoals: [affectedID],
            created_at: '2026-07-20T04:01:35.457204376+02:00',
            load: 0.5,
            plan_id: 'c02a86da-0b2d-4294-8eb3-1f3839c86879',
            source: 'assumption',
            statement: 'The memory search may not contain prior transcript answers.',
            status: 'active',
          },
        ]}
      />,
    )

    expect(
      screen.getByText('The memory search may not contain prior transcript answers.'),
    ).toBeVisible()
    expect(screen.getByText('50%')).toBeVisible()
    expect(
      screen.getByText('Ion inferred this; it is not confirmed yet'),
    ).toBeVisible()
    expect(container).not.toHaveTextContent(affectedID)
    expect(container).not.toHaveTextContent('{"affected_subgoals"')
  })

  it('renders nested counts as labeled values instead of encoded objects', () => {
    const { container } = render(
      <StructuredValue
        operation="prediction.list"
        value={{ counts: { memory_search: 0 }, threshold: 3 }}
      />,
    )

    expect(screen.getByText('Results by action')).toBeVisible()
    expect(screen.getByText('Memory search')).toBeVisible()
    expect(screen.getByText('3 repeated results')).toBeVisible()
    expect(container).not.toHaveTextContent('{"memory_search":0}')
  })

  it('summarizes encoded tool evidence without exposing its JSON as a title', () => {
    const encoded = JSON.stringify({
      name: 'memory_search',
      args: { query: 'identity' },
      result: { memories: [] },
      failure_class: '',
    })
    const { container } = render(
      <StructuredValue
        operation="memory.search"
        value={{
          memories: [
            { content: encoded, pinned: false, type: '0x05' },
            { statement: encoded, pinned: false, type: '0x05' },
          ],
          truncated: false,
        }}
      />,
    )

    expect(screen.getAllByText('Memory search completed')).toHaveLength(2)
    expect(container).not.toHaveTextContent('"failure_class"')
    expect(container).not.toHaveTextContent('"query":"identity"')
  })

  it('labels truncated tool evidence without showing malformed JSON', () => {
    const truncated = '{"args":{"url":"https://example.test"},"name":"web_fetch","result":"cut off'
    const { container } = render(
      <StructuredValue
        operation="memory.search"
        value={{ memories: [{ content: truncated, type: '0x05' }] }}
      />,
    )

    expect(screen.getByText('Web fetch activity')).toBeVisible()
    expect(container).not.toHaveTextContent('"args"')
  })

  it('shows durable liveness consequences without leading with raw meters', () => {
    const { container } = render(
      <StructuredValue
        operation="liveness.get"
        value={{
          emotional: { frustration: 0.91, confidence: 0.31 },
          presence: {
            since_you_were_away: [{
              kind: 'I learned',
              summary: 'One bounded investigation completed with verified evidence.',
              evidence_id: 'work-1',
            }],
          },
          decision: {
            same_strategy_retries: 1,
            causes: [{
              code: 'strategy_revision',
              explanation: 'The same-strategy retry limit was reduced after evidence stagnated.',
            }],
          },
          aesthetic: { label: 'simple, coherent, low-burden solutions' },
          repair: { lesson: 'Change approach after the previous probe fails.' },
        }}
      />,
    )

    expect(screen.getByText('Since you were away')).toBeVisible()
    expect(screen.getByText(/bounded investigation completed/)).toBeVisible()
    expect(screen.getByText(/retry limit was reduced/)).toBeVisible()
    expect(screen.getByText('simple, coherent, low-burden solutions')).toBeVisible()
    expect(screen.getByText(/Change approach after/)).toBeVisible()
    expect(container).not.toHaveTextContent('0.91')
    expect(container).not.toHaveTextContent('0.31')
  })

  it('renders the since-away brief by evidence category without raw liveness state', () => {
    const { container } = render(
      <StructuredValue
        operation="continuity.brief"
        value={{
          status: 'ready',
          period: '24h',
          sections: [
            {
              kind: 'completed_work',
              label: 'Completed work',
              items: [{
                summary: 'A task completed with a durable result',
                occurred_at: '2026-07-25T12:00:00Z',
                evidence_id: 'event-1',
              }],
            },
            {
              kind: 'changed_files',
              label: 'Changed files',
              items: [{
                summary: 'internal/service.go',
                occurred_at: '2026-07-25T11:00:00Z',
                evidence_id: 'event-2',
              }],
            },
            {
              kind: 'pending_questions',
              label: 'Pending questions',
              items: [{
                summary: 'Decision waiting for browser_submit',
                occurred_at: '2026-07-25T10:00:00Z',
                evidence_id: 'event-3',
              }],
            },
          ],
          emotional: { frustration: 0.9 },
        }}
      />,
    )

    expect(screen.getByText('Completed work')).toBeVisible()
    expect(screen.getByText('Changed files')).toBeVisible()
    expect(screen.getByText('internal/service.go')).toBeVisible()
    expect(screen.getByText('Pending questions')).toBeVisible()
    expect(container).not.toHaveTextContent('0.9')
  })

  it('does not embellish an empty since-away period', () => {
    const { container } = render(
      <StructuredValue
        operation="continuity.brief"
        value={{ status: 'no_activity', period: '7d', sections: [] }}
      />,
    )
    expect(screen.getByText(/No verified work, failure, decision/)).toBeVisible()
  })

  it('shows wide-work lanes and live specialist progress without raw packets', () => {
    const { container } = render(
      <StructuredValue
        operation="supervisor.list"
        value={[{
          id: '11111111-1111-4111-8111-111111111111',
          status: 'working',
          budget: { max_parallel: 20 },
          usage: { tokens: 1200, tool_calls: 4, cost_cents: 8, provider_cents: 0 },
          tasks: [
            {
              status: 'running',
              progress: 45,
              packet: { id: 'frontend', title: 'Build the responsive shell', specialist: 'frontend' },
              attempts: [{ id: 'attempt-1' }],
            },
            {
              status: 'blocked',
              progress: 20,
              blocking_reason: 'A decision is required before deployment.',
              packet: { id: 'operations', title: 'Prepare deployment', specialist: 'operations' },
              attempts: [{ id: 'attempt-2' }],
            },
          ],
        }]}
      />,
    )

    expect(screen.getByText('1 active / 20 lanes')).toBeVisible()
    expect(screen.getByText('Build the responsive shell')).toBeVisible()
    expect(screen.getByText('Frontend · Running')).toBeVisible()
    expect(screen.getByText('45% · 1 attempt')).toBeVisible()
    expect(screen.getByText(/decision is required/)).toBeVisible()
    expect(container).not.toHaveTextContent('11111111-1111-4111-8111-111111111111')
  })

  it('steers and cancels supervised work while exposing attention', async () => {
    const command = vi.fn(async () => undefined)
    const { container } = render(
      <StructuredValue
        onSupervisorCommand={command}
        operation="supervisor.list"
        value={[{
          id: '11111111-1111-4111-8111-111111111111',
          status: 'working',
          budget: { max_parallel: 20 },
          usage: {},
          tasks: [{
            status: 'waiting_evidence',
            progress: 90,
            blocking_reason: 'Verified evidence is still required.',
            packet: {
              id: 'review',
              title: 'Verify the release',
              specialist: 'review',
            },
          }],
        }]}
      />,
    )
    const supervisor = within(container)
    expect(
      supervisor.getByText(
        '1 workstream need a decision or verified evidence.',
      ),
    ).toBeVisible()
    fireEvent.change(supervisor.getByLabelText('Guidance for this outcome'), {
      target: { value: 'Use the current verification manifest.' },
    })
    fireEvent.click(
      supervisor.getByRole('button', { name: 'Add guidance' }),
    )
    await waitFor(() => {
      expect(command).toHaveBeenCalledWith('supervisor.steer', {
        run_id: '11111111-1111-4111-8111-111111111111',
        instruction: 'Use the current verification manifest.',
      })
    })
    fireEvent.click(
      supervisor.getByRole('button', { name: 'Cancel supervised work' }),
    )
    await waitFor(() => {
      expect(command).toHaveBeenCalledWith('supervisor.cancel', {
        run_id: '11111111-1111-4111-8111-111111111111',
      })
    })
  })
})
