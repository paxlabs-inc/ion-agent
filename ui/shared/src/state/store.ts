import type { EventEnvelope, RecoveryEnvelope } from '../generated/protocol'
import {
  applyEvent,
  applyRecovery,
  emptyOperatorState,
  type OperatorState,
} from './events'

export class OperatorEventStore {
  private state: OperatorState = emptyOperatorState()
  private readonly listeners = new Set<() => void>()

  getSnapshot = (): OperatorState => this.state

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  recover(recovery: RecoveryEnvelope): void {
    this.update(applyRecovery(this.state, recovery))
  }

  accept(event: EventEnvelope): void {
    this.update(applyEvent(this.state, event))
  }

  private update(next: OperatorState): void {
    if (next === this.state) return
    this.state = next
    for (const listener of this.listeners) listener()
  }
}
