import {
  PROTOCOL_VERSION,
  assertEventEnvelope,
  assertResponseEnvelope,
  type EventEnvelope,
  type LocalServerFrame,
  type RecoveryEnvelope,
  type RequestEnvelope,
  type RequestTransport,
  type ResponseEnvelope,
} from '@matrixmcl/ion-shared'
import { createConnection, type Socket } from 'node:net'

interface PendingRequest {
  resolve(value: ResponseEnvelope): void
  reject(reason: Error): void
}

export interface LocalClientCallbacks {
  recovery(recovery: RecoveryEnvelope): void
  event(event: EventEnvelope): void
  degraded(error: Error): void
}

// LocalControlPlaneTransport owns one bounded newline-framed UDS connection.
export class LocalControlPlaneTransport implements RequestTransport {
  readonly actorID: string
  private readonly socket: Socket
  private readonly callbacks: LocalClientCallbacks
  private readonly pending: PendingRequest[] = []
  private buffer = ''
  private closed = false

  private constructor(socket: Socket, actorID: string, callbacks: LocalClientCallbacks) {
    this.socket = socket
    this.actorID = actorID
    this.callbacks = callbacks
    socket.setEncoding('utf8')
    socket.on('data', (chunk: string) => this.accept(chunk))
    socket.on('error', (error) => this.fail(error))
    socket.on('close', () => {
      if (!this.closed) this.fail(new Error('local control plane disconnected'))
    })
  }

  static connect(
    socketPath: string,
    capability: string,
    callbacks: LocalClientCallbacks,
    timeoutMilliseconds = 5_000,
  ): Promise<LocalControlPlaneTransport> {
    return new Promise((resolve, reject) => {
      const socket = createConnection(socketPath)
      const timeout = setTimeout(() => {
        socket.destroy()
        reject(new Error('local control plane startup timed out'))
      }, timeoutMilliseconds)
      let buffer = ''
      const fail = (error: Error) => {
        clearTimeout(timeout)
        reject(error)
      }
      socket.once('error', fail)
      socket.once('connect', () => {
        socket.write(`${JSON.stringify({ capability })}\n`)
      })
      const ready = (chunk: Buffer | string) => {
        buffer += String(chunk)
        const newline = buffer.indexOf('\n')
        if (newline < 0) return
        const line = buffer.slice(0, newline)
        const trailing = buffer.slice(newline + 1)
        let frame: LocalServerFrame
        try {
          frame = JSON.parse(line) as LocalServerFrame
        } catch {
          fail(new Error('invalid local ready frame'))
          return
        }
        if (
          frame.type !== 'ready' ||
          frame.protocol_version !== PROTOCOL_VERSION ||
          frame.actor_id === undefined
        ) {
          fail(new Error(frame.error?.message ?? 'local capability rejected'))
          return
        }
        clearTimeout(timeout)
        socket.off('error', fail)
        socket.off('data', ready)
        const transport = new LocalControlPlaneTransport(
          socket,
          frame.actor_id,
          callbacks,
        )
        if (trailing !== '') transport.accept(trailing)
        resolve(transport)
      }
      socket.on('data', ready)
    })
  }

  rpc<T>(request: RequestEnvelope): Promise<ResponseEnvelope<T>> {
    if (this.closed) return Promise.reject(new Error('local transport is closed'))
    return new Promise((resolve, reject) => {
      this.pending.push({
        resolve: (value) => resolve(value as ResponseEnvelope<T>),
        reject,
      })
      this.socket.write(`${JSON.stringify({ type: 'rpc', request })}\n`)
    })
  }

  subscribe(afterSequence: number): void {
    this.socket.write(`${JSON.stringify({ type: 'subscribe', after_sequence: afterSequence })}\n`)
  }

  acknowledge(clientID: string, sequence: number): void {
    this.socket.write(`${JSON.stringify({ type: 'ack', client_id: clientID, sequence })}\n`)
  }

  close(): void {
    this.closed = true
    this.socket.end()
    this.rejectPending(new Error('local transport closed'))
  }

  private accept(chunk: string): void {
    this.buffer += chunk
    while (true) {
      const newline = this.buffer.indexOf('\n')
      if (newline < 0) return
      const line = this.buffer.slice(0, newline)
      this.buffer = this.buffer.slice(newline + 1)
      if (line.trim() === '') continue
      this.acceptFrame(line)
    }
  }

  private acceptFrame(line: string): void {
    let frame: LocalServerFrame
    try {
      frame = JSON.parse(line) as LocalServerFrame
    } catch {
      this.fail(new Error('malformed local control-plane frame'))
      return
    }
    if (frame.protocol_version !== PROTOCOL_VERSION) {
      this.fail(new Error('local control-plane protocol mismatch'))
      return
    }
    if (frame.type === 'rpc' && frame.response !== undefined) {
      try {
        assertResponseEnvelope(frame.response)
      } catch {
        this.fail(new Error('invalid local RPC response'))
        return
      }
      const pending = this.pending.shift()
      if (pending === undefined) {
        this.fail(new Error('unsolicited local RPC response'))
        return
      }
      pending.resolve(frame.response)
      return
    }
    if (frame.type === 'recovery' && frame.recovery !== undefined) {
      this.callbacks.recovery(frame.recovery)
      return
    }
    if (frame.type === 'event' && frame.event !== undefined) {
      try {
        assertEventEnvelope(frame.event)
      } catch {
        this.fail(new Error('invalid local event frame'))
        return
      }
      this.callbacks.event(frame.event)
      return
    }
    if (frame.type === 'ack') return
    if (frame.type === 'error') {
      this.fail(new Error(frame.error?.message ?? 'local control-plane error'))
      return
    }
    this.fail(new Error('incomplete local control-plane frame'))
  }

  private fail(error: Error): void {
    if (this.closed) return
    this.closed = true
    this.socket.destroy()
    this.rejectPending(error)
    this.callbacks.degraded(error)
  }

  private rejectPending(error: Error): void {
    for (const pending of this.pending.splice(0)) pending.reject(error)
  }
}
