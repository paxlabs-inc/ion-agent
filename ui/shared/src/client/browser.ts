import {
  PROTOCOL_VERSION,
  assertResponseEnvelope,
  isEventEnvelope,
  type BrowserStreamFrame,
  type EventEnvelope,
  type Operation,
  type RecoveryEnvelope,
  type RequestEnvelope,
  type ResponseEnvelope,
  type Scope,
} from '../generated/protocol'

export interface BrowserClientOptions {
  base_url?: string
  websocket_url?: string
  fetch_impl?: typeof fetch
  websocket_factory?: (url: string) => WebSocket
}

export interface EventConnection {
  close(): void
}

export interface EventCallbacks {
  recovery(recovery: RecoveryEnvelope): void
  event(event: EventEnvelope): void
  degraded(error: Error): void
}

interface TicketResponse {
  ticket: string
  expires_at: string
}

export class BrowserControlPlaneClient {
  readonly actor_id: string
  private readonly baseURL: string
  private readonly websocketURL: string
  private readonly fetchImpl: typeof fetch
  private readonly websocketFactory: (url: string) => WebSocket

  constructor(actorID: string, options: BrowserClientOptions = {}) {
    this.actor_id = actorID
    this.baseURL = options.base_url ?? ''
    this.websocketURL = options.websocket_url ?? deriveWebSocketURL(this.baseURL)
    this.fetchImpl = options.fetch_impl ?? fetch.bind(globalThis)
    this.websocketFactory = options.websocket_factory ?? ((url) => new WebSocket(url))
  }

  query<T>(
    operation: Operation,
    payload: unknown,
    scope: Omit<Scope, 'actor_id'> = {},
  ): Promise<ResponseEnvelope<T>> {
    return this.rpc<T>({
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
    const request: RequestEnvelope = {
      protocol_version: PROTOCOL_VERSION,
      request_id: crypto.randomUUID(),
      kind: 'command',
      operation,
      scope: { actor_id: this.actor_id, ...scope },
      idempotency_key: idempotencyKey,
      payload,
      ...(expectedRevision === undefined ? {} : { expected_revision: expectedRevision }),
    }
    return this.rpc<T>(request)
  }

  async connect(
    afterSequence: number,
    clientID: string,
    callbacks: EventCallbacks,
  ): Promise<EventConnection> {
    const ticket = await this.issueTicket()
    const url = new URL('/v1/events', this.websocketURL)
    url.searchParams.set('after', String(afterSequence))
    url.searchParams.set('ticket', ticket.ticket)
    const socket = this.websocketFactory(url.toString())
    let closed = false
    socket.addEventListener('message', (message) => {
      try {
        const parsed: unknown = JSON.parse(String(message.data))
        if (!isRecord(parsed) || typeof parsed.type !== 'string') {
          throw new TypeError('invalid event stream frame')
        }
        const frame = parsed as unknown as BrowserStreamFrame
        if (frame.type === 'recovery' && isRecovery(frame.recovery)) {
          callbacks.recovery(frame.recovery)
          acknowledge(socket, clientID, frame.recovery.replay.latest_sequence)
          return
        }
        if (frame.type === 'event' && isEventEnvelope(frame.event)) {
          callbacks.event(frame.event)
          acknowledge(socket, clientID, frame.event.sequence)
          return
        }
        if (frame.type === 'error' && frame.error !== undefined) {
          throw new Error(frame.error.message)
        }
        throw new TypeError('incomplete event stream frame')
      } catch (error) {
        callbacks.degraded(asError(error))
        socket.close()
      }
    })
    socket.addEventListener('close', () => {
      if (!closed) callbacks.degraded(new Error('event stream closed'))
    })
    socket.addEventListener('error', () => {
      if (!closed) callbacks.degraded(new Error('event stream transport error'))
    })
    return {
      close() {
        closed = true
        socket.close(1000, 'client shutdown')
      },
    }
  }

  private async rpc<T>(request: RequestEnvelope): Promise<ResponseEnvelope<T>> {
    const response = await this.fetchImpl(this.baseURL + '/v1/rpc', {
      method: 'POST',
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        ...(request.kind === 'command'
          ? { 'X-Ion-CSRF': readCookie('__Host-ion_csrf') }
          : {}),
      },
      body: JSON.stringify(request),
    })
    const payload: unknown = await response.json()
    assertResponseEnvelope(payload)
    return payload as ResponseEnvelope<T>
  }

  private async issueTicket(): Promise<TicketResponse> {
    const response = await this.fetchImpl(this.baseURL + '/v1/ws-ticket', {
      method: 'POST',
      credentials: 'include',
      headers: {
        'X-Ion-CSRF': readCookie('__Host-ion_csrf'),
      },
    })
    if (!response.ok) throw new Error('WebSocket ticket request failed')
    const value: unknown = await response.json()
    if (
      !isRecord(value) ||
      typeof value.ticket !== 'string' ||
      typeof value.expires_at !== 'string'
    ) {
      throw new TypeError('invalid WebSocket ticket response')
    }
    return { ticket: value.ticket, expires_at: value.expires_at }
  }
}

function acknowledge(socket: WebSocket, clientID: string, sequence: number): void {
  if (socket.readyState !== WebSocket.OPEN) return
  socket.send(JSON.stringify({ type: 'ack', client_id: clientID, sequence }))
}

function deriveWebSocketURL(baseURL: string): string {
  if (baseURL !== '') {
    const url = new URL(baseURL)
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
    return url.toString()
  }
  if (typeof location === 'undefined') return 'ws://127.0.0.1'
  return `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}`
}

function readCookie(name: string): string {
  if (typeof document === 'undefined') return ''
  const prefix = `${name}=`
  for (const item of document.cookie.split(';')) {
    const value = item.trim()
    if (value.startsWith(prefix)) return decodeURIComponent(value.slice(prefix.length))
  }
  return ''
}

function isRecovery(value: unknown): value is RecoveryEnvelope {
  if (!isRecord(value) || !isRecord(value.replay)) return false
  const replay = value.replay
  return (
    typeof replay.after_sequence === 'number' &&
    typeof replay.earliest_sequence === 'number' &&
    typeof replay.latest_sequence === 'number' &&
    typeof replay.head_sequence === 'number' &&
    typeof replay.gap === 'boolean' &&
    Array.isArray(replay.events) &&
    replay.events.every(isEventEnvelope)
  )
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value))
}
