import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import {
  failureMessage,
  isTerminalTurnEvent,
  Message,
} from '../features/chat/ChatHost'
import type { EventEnvelope } from '@matrixmcl/ion-shared'

function turnEvent(
  type: 'turn.failed' | 'turn.incomplete' | 'turn.completed',
  payload: Record<string, unknown>,
): EventEnvelope {
  return {
    sequence: 1,
    event_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    type,
    occurred_at: '2026-07-25T12:00:00.000Z',
    correlation: {
      actor_id: '11111111-1111-4111-8111-111111111111',
      session_id: '22222222-2222-4222-8222-222222222222',
      turn_id: '33333333-3333-4333-8333-333333333333',
    },
    payload,
  }
}

describe('chat message controls', () => {
  it('shows a collapsed reasoning summary and the complete assistant action set', () => {
    render(
      <Message
        message={{ id: 'turn:one', role: 'assistant', content: 'A useful answer.' }}
        onEdit={vi.fn()}
        onFork={vi.fn()}
        reasoning="I checked the available evidence."
      />,
    )
    expect(screen.getByText('Reasoning summary')).toBeVisible()
    expect(screen.getByText('I checked the available evidence.')).not.toBeVisible()
    for (const name of ['Copy', 'Fork', 'Share', 'Read aloud']) {
      expect(screen.getByRole('button', { name })).toBeInTheDocument()
    }
  })

  it('adds editing to user messages', () => {
    render(
      <Message
        message={{ id: 'user-one', role: 'user', content: 'Original prompt' }}
        onEdit={vi.fn()}
        onFork={vi.fn()}
      />,
    )
    expect(screen.getByRole('button', { name: 'Edit' })).toBeInTheDocument()
  })

  it('turns provider payment refusal into actionable user guidance', () => {
    expect(failureMessage({
      error_class: 'permanent',
      error_code: 'provider_payment_required',
    })).toEqual({
      title: 'The model provider needs attention',
      detail:
        'The provider refused this request because the account needs payment or credits. Update the provider account, then try again.',
    })
  })

  it('does not present a recoverable incomplete checkpoint as a terminal failure', () => {
    expect(isTerminalTurnEvent(turnEvent('turn.incomplete', {
      phase: 'answer_validation',
      final_honest_partial: false,
    }))).toBe(false)
    expect(isTerminalTurnEvent(turnEvent('turn.incomplete', {
      phase: 'answer_validation',
      final_honest_partial: true,
    }))).toBe(true)
    expect(isTerminalTurnEvent(turnEvent('turn.failed', {
      error_class: 'permanent',
    }))).toBe(true)
    expect(isTerminalTurnEvent(turnEvent('turn.completed', {
      state: 'completed',
    }))).toBe(false)
  })
})
