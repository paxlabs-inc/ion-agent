import {
  OPERATION_KINDS,
  PROTOCOL_VERSION,
  type Operation,
  type RequestEnvelope,
  type ResponseEnvelope,
  type Scope,
} from '../generated/protocol'

export interface RequestTransport {
  rpc<T>(request: RequestEnvelope): Promise<ResponseEnvelope<T>>
}

// ControlPlaneClient is the shared request builder used by browser and TUI
// transports. Operation kinds always come from the generated server catalog.
export class ControlPlaneClient {
  readonly actor_id: string
  private readonly transport: RequestTransport

  constructor(actorID: string, transport: RequestTransport) {
    this.actor_id = actorID
    this.transport = transport
  }

  query<T>(
    operation: Operation,
    payload: unknown,
    scope: Omit<Scope, 'actor_id'> = {},
  ): Promise<ResponseEnvelope<T>> {
    if (OPERATION_KINDS[operation] !== 'query') {
      throw new TypeError(`${operation} is not a query`)
    }
    return this.transport.rpc<T>({
      protocol_version: PROTOCOL_VERSION,
      request_id: crypto.randomUUID(),
      kind: 'query',
      operation,
      scope: { actor_id: this.actor_id, ...scope },
      payload,
    })
  }

  command<T>(
    operation: Operation,
    payload: unknown,
    idempotencyKey: string,
    scope: Omit<Scope, 'actor_id'> = {},
    expectedRevision?: number,
  ): Promise<ResponseEnvelope<T>> {
    if (OPERATION_KINDS[operation] !== 'command') {
      throw new TypeError(`${operation} is not a command`)
    }
    return this.transport.rpc<T>({
      protocol_version: PROTOCOL_VERSION,
      request_id: crypto.randomUUID(),
      kind: 'command',
      operation,
      scope: { actor_id: this.actor_id, ...scope },
      idempotency_key: idempotencyKey,
      payload,
      ...(expectedRevision === undefined ? {} : { expected_revision: expectedRevision }),
    })
  }
}
