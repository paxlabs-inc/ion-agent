import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useOperator } from '../../app/operator-context'

interface DesktopCapability {
  kind: string
  available: boolean
  degraded: boolean
  reason?: string
}

interface DesktopStatus {
  protocol_version: string
  mode: 'personal' | 'clean'
  state: string
  width: number
  height: number
  started_at: string
  last_error?: string
  capabilities: DesktopCapability[]
}

interface DesktopControlLease {
  protocol_version: string
  lease_id?: string
  target: {
    actor_id: string
    session_id?: string
    resource_kind: 'desktop'
    resource_id: string
  }
  owner: {
    turn_id?: string
    task_id?: string
    agent_id: string
    tool_event_id?: string
    action: string
    revision: number
  }
  state: 'available' | 'active' | 'released' | 'expired'
  authority: 'executor' | 'operator'
  revision: number
  expires_at?: string
  reconciliation: string
}

interface DesktopTicket {
  ticket: string
  kind: 'view' | 'input'
  expires_at: string
}

type DesktopInput =
  | { kind: 'move'; x: number; y: number }
  | { kind: 'click'; x: number; y: number; button: 'left' | 'right' | 'middle'; count: number }
  | { kind: 'scroll'; x: number; y: number; direction: 'up' | 'down' | 'left' | 'right'; amount: number }
  | { kind: 'type'; text: string }
  | { kind: 'key'; key: string; modifiers?: string[] }
  | { kind: 'hotkey'; keys: string[] }

export function PrivateDesktop({ active }: { active: boolean }) {
  const operator = useOperator()
  const sessionID = operator.sessionID
  const [notice, setNotice] = useState<string>()
  const [frameNonce, setFrameNonce] = useState(0)
  const [frameReady, setFrameReady] = useState(false)
  const frameFailures = useRef(0)
  const frameTimer = useRef<number | undefined>(undefined)
  const pointerSentAt = useRef(0)

  const status = useQuery({
    queryKey: ['private-desktop', 'status'],
    enabled: active,
    retry: false,
    refetchInterval: active ? 3_000 : false,
    queryFn: async () => {
      const response = await fetch('/v1/computer/status', {
        credentials: 'same-origin',
        headers: { Accept: 'application/json' },
      })
      const result = await response.json() as DesktopStatus & { reason?: string }
      if (!response.ok) throw new Error(result.reason ?? 'Private Computer is unavailable')
      return result
    },
  })

  const control = useQuery({
    queryKey: ['private-desktop', 'control', sessionID],
    enabled: active && sessionID !== undefined,
    refetchInterval: active && sessionID !== undefined ? 1_000 : false,
    queryFn: async () => {
      if (sessionID === undefined) throw new Error('A conversation is required')
      const response = await operator.client.query<DesktopControlLease>(
        'computer.control.get',
        {
          resource_kind: 'desktop',
          resource_id: sessionID,
          target_revision: 1,
        },
        { session_id: sessionID },
      )
      if (response.error !== undefined || response.result === undefined) {
        throw new Error(response.error?.message ?? 'Desktop control is unavailable')
      }
      return response.result
    },
  })

  const viewTicket = useQuery({
    queryKey: ['private-desktop', 'view-ticket', sessionID],
    enabled: active && status.data?.state === 'ready' && sessionID !== undefined,
    refetchInterval: active ? 20_000 : false,
    staleTime: 15_000,
    retry: false,
    queryFn: async () => issueTicket('view', sessionID),
  })

  const inputTicket = useQuery({
    queryKey: [
      'private-desktop',
      'input-ticket',
      sessionID,
      control.data?.lease_id,
      control.data?.revision,
    ],
    enabled: active &&
      sessionID !== undefined &&
      control.data?.state === 'active' &&
      control.data.authority === 'operator' &&
      control.data.lease_id !== undefined,
    refetchInterval: active ? 20_000 : false,
    staleTime: 15_000,
    retry: false,
    queryFn: async () => issueTicket(
      'input',
      sessionID,
      control.data?.lease_id,
      control.data?.revision,
    ),
  })

  const frameURL = useMemo(() => {
    if (viewTicket.data?.ticket === undefined) return undefined
    return `/v1/computer/frame?ticket=${encodeURIComponent(viewTicket.data.ticket)}&frame=${String(frameNonce)}`
  }, [frameNonce, viewTicket.data?.ticket])

  useEffect(() => {
    if (!active) {
      setFrameReady(false)
      setNotice(undefined)
      frameFailures.current = 0
      if (frameTimer.current !== undefined) {
        window.clearTimeout(frameTimer.current)
        frameTimer.current = undefined
      }
    }
  }, [active])

  useEffect(() => () => {
    if (frameTimer.current !== undefined) {
      window.clearTimeout(frameTimer.current)
    }
  }, [])

  const runControl = async (
    operation:
      | 'computer.control.acquire'
      | 'computer.control.renew'
      | 'computer.control.release',
  ) => {
    if (sessionID === undefined || control.data === undefined) return
    const current = control.data
    const target = {
      resource_kind: 'desktop',
      resource_id: sessionID,
      target_revision: current.owner.revision || 1,
    }
    const payload = operation === 'computer.control.acquire'
      ? {
          ...target,
          owner: current.owner,
          expected_lease_revision: current.revision,
          ttl_seconds: 90,
        }
      : {
          ...target,
          lease_id: current.lease_id,
          expected_lease_revision: current.revision,
          ...(operation === 'computer.control.renew' ? { ttl_seconds: 90 } : {}),
        }
    const response = await operator.command<DesktopControlLease>(
      operation,
      payload,
      crypto.randomUUID(),
      { session_id: sessionID },
    )
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    setNotice(operation === 'computer.control.acquire'
      ? 'You have control. Ion is paused at the desktop action boundary.'
      : operation === 'computer.control.renew'
        ? 'Desktop control renewed.'
        : 'Control returned to Ion.')
    await control.refetch()
  }

  const sendInput = async (input: DesktopInput) => {
    const ticket = inputTicket.data?.ticket
    if (ticket === undefined) return
    const response = await fetch(
      `/v1/computer/input?ticket=${encodeURIComponent(ticket)}`,
      {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
          'X-Ion-CSRF': readCookie('__Host-ion_csrf'),
        },
        body: JSON.stringify(input),
      },
    )
    if (!response.ok) {
      setNotice(response.status === 409
        ? 'Desktop control changed. Reconnecting authority before accepting input.'
        : 'Desktop input was rejected.')
      await Promise.all([control.refetch(), inputTicket.refetch()])
    }
  }

  const coordinates = (event: {
    clientX: number
    clientY: number
    currentTarget: HTMLElement
  }) => {
    const bounds = event.currentTarget.getBoundingClientRect()
    const width = status.data?.width ?? 1
    const height = status.data?.height ?? 1
    return {
      x: Math.max(0, Math.min(width - 1, ((event.clientX - bounds.left) / bounds.width) * width)),
      y: Math.max(0, Math.min(height - 1, ((event.clientY - bounds.top) / bounds.height) * height)),
    }
  }

  const operatorOwnsControl = control.data?.state === 'active' &&
    control.data.authority === 'operator'
  const streamAvailable = status.data?.capabilities.some(
    (capability) => capability.kind === 'desktop_stream' && capability.available,
  ) ?? false

  if (sessionID === undefined) {
    return (
      <section className="private-desktop" data-state="unavailable">
        <DesktopMessage
          detail="Start or open a conversation so the desktop can be bound to an authenticated session."
          title="Choose a conversation to open Computer"
        />
      </section>
    )
  }

  if (status.isPending) {
    return (
      <section className="private-desktop" data-state="connecting">
        <DesktopMessage
          detail="Connecting to the private desktop host and checking the real frame path."
          title="Starting Computer"
        />
      </section>
    )
  }

  if (status.isError || !streamAvailable) {
    return (
      <section className="private-desktop" data-state="unavailable">
        <DesktopMessage
          detail={status.error instanceof Error
            ? status.error.message
            : 'The host did not provide an authenticated desktop stream.'}
          title="Computer unavailable"
        />
      </section>
    )
  }

  return (
    <section
      aria-label="Live private computer"
      className="private-desktop"
      data-control={operatorOwnsControl ? 'operator' : 'ion'}
      data-state={frameReady ? 'live' : 'connecting'}
    >
      <header className="private-desktop-toolbar">
        <div>
          <span className="private-desktop-live" aria-hidden="true" />
          <strong>{frameReady ? 'Live desktop' : 'Connecting to desktop'}</strong>
          <span>{humanize(status.data.mode)} · {status.data.width}×{status.data.height}</span>
        </div>
        <div>
          <span>{operatorOwnsControl ? 'You have control' : 'Ion has control'}</span>
          {operatorOwnsControl ? (
            <>
              <button onClick={() => { void runControl('computer.control.renew') }} type="button">
                Renew
              </button>
              <button onClick={() => { void runControl('computer.control.release') }} type="button">
                Return control
              </button>
            </>
          ) : (
            <button
              disabled={control.data === undefined || control.isError}
              onClick={() => { void runControl('computer.control.acquire') }}
              type="button"
            >
              Take control
            </button>
          )}
        </div>
      </header>
      <div
        aria-label={operatorOwnsControl
          ? 'Interactive private desktop. Mouse and keyboard input are active.'
          : 'Live private desktop. Take control to use mouse and keyboard.'}
        className="private-desktop-viewport"
        onContextMenu={(event) => {
          if (!operatorOwnsControl) return
          event.preventDefault()
          const point = coordinates(event)
          void sendInput({ kind: 'click', ...point, button: 'right', count: 1 })
        }}
        onKeyDown={(event) => {
          if (!operatorOwnsControl) return
          if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
            event.preventDefault()
            void sendInput({ kind: 'type', text: event.key })
            return
          }
          const key = desktopKey(event.key)
          if (key === undefined) return
          event.preventDefault()
          const modifiers = [
            ...(event.ctrlKey ? ['ctrl'] : []),
            ...(event.altKey ? ['alt'] : []),
            ...(event.shiftKey ? ['shift'] : []),
            ...(event.metaKey ? ['meta'] : []),
          ]
          void sendInput({ kind: 'key', key, modifiers })
        }}
        onPointerDown={(event) => {
          if (!operatorOwnsControl) return
          event.currentTarget.focus()
          const point = coordinates(event)
          const button = event.button === 2 ? 'right' : event.button === 1 ? 'middle' : 'left'
          void sendInput({ kind: 'click', ...point, button, count: event.detail > 1 ? 2 : 1 })
        }}
        onPointerMove={(event) => {
          if (!operatorOwnsControl) return
          const now = performance.now()
          if (now - pointerSentAt.current < 50) return
          pointerSentAt.current = now
          void sendInput({ kind: 'move', ...coordinates(event) })
        }}
        onWheel={(event) => {
          if (!operatorOwnsControl) return
          event.preventDefault()
          const horizontal = Math.abs(event.deltaX) > Math.abs(event.deltaY)
          const direction = horizontal
            ? event.deltaX < 0 ? 'left' : 'right'
            : event.deltaY < 0 ? 'up' : 'down'
          void sendInput({
            kind: 'scroll',
            ...coordinates(event),
            direction,
            amount: Math.max(1, Math.min(20, Math.ceil(
              Math.abs(horizontal ? event.deltaX : event.deltaY) / 40,
            ))),
          })
        }}
        role="application"
        tabIndex={operatorOwnsControl ? 0 : -1}
      >
        {frameURL === undefined ? null : (
          <img
            alt="Live private computer desktop"
            draggable={false}
            onError={() => {
              setFrameReady(false)
              setNotice('Desktop frame interrupted. Reconnecting.')
              if (frameTimer.current !== undefined) {
                window.clearTimeout(frameTimer.current)
              }
              const delay = frameRetryDelay(frameFailures.current)
              frameFailures.current += 1
              frameTimer.current = window.setTimeout(() => {
                frameTimer.current = undefined
                void viewTicket.refetch()
              }, delay)
            }}
            onLoad={() => {
              setFrameReady(true)
              frameFailures.current = 0
              if (frameTimer.current !== undefined) {
                window.clearTimeout(frameTimer.current)
              }
              frameTimer.current = window.setTimeout(() => {
                frameTimer.current = undefined
                setFrameNonce((value) => value + 1)
              }, 250)
            }}
            src={frameURL}
          />
        )}
        {frameReady ? null : (
          <div className="private-desktop-loading" role="status">
            Waiting for the first verified desktop frame
          </div>
        )}
      </div>
      <footer>
        <span>{operatorOwnsControl
          ? 'Input is lease-bound to this session.'
          : 'Watch mode is read-only.'}</span>
        <span>{control.data?.reconciliation.replaceAll('_', ' ') ?? 'Checking control authority'}</span>
      </footer>
      {notice === undefined ? null : <p className="private-desktop-notice" role="status">{notice}</p>}
    </section>
  )
}

function DesktopMessage({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="private-desktop-message" role="status">
      <strong>{title}</strong>
      <p>{detail}</p>
    </div>
  )
}

async function issueTicket(
  kind: 'view' | 'input',
  sessionID: string | undefined,
  leaseID?: string,
  leaseRevision?: number,
): Promise<DesktopTicket> {
  if (sessionID === undefined) throw new Error('A conversation is required')
  const response = await fetch('/v1/computer/ticket', {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Ion-CSRF': readCookie('__Host-ion_csrf'),
    },
    body: JSON.stringify({
      kind,
      session_id: sessionID,
      ...(leaseID === undefined ? {} : { lease_id: leaseID }),
      ...(leaseRevision === undefined ? {} : { lease_revision: leaseRevision }),
    }),
  })
  const result = await response.json() as DesktopTicket & { error?: string }
  if (!response.ok) throw new Error(result.error ?? 'Desktop ticket is unavailable')
  return result
}

function readCookie(name: string): string {
  const prefix = `${name}=`
  for (const value of document.cookie.split(';')) {
    const trimmed = value.trim()
    if (trimmed.startsWith(prefix)) {
      return decodeURIComponent(trimmed.slice(prefix.length))
    }
  }
  return ''
}

function desktopKey(key: string): string | undefined {
  const mapped: Record<string, string> = {
    ArrowDown: 'down',
    ArrowLeft: 'left',
    ArrowRight: 'right',
    ArrowUp: 'up',
    Backspace: 'backspace',
    Delete: 'delete',
    End: 'end',
    Enter: 'enter',
    Escape: 'esc',
    Home: 'home',
    PageDown: 'pagedown',
    PageUp: 'pageup',
    Tab: 'tab',
  }
  return mapped[key]
}

function humanize(value: string): string {
  return value
    .replaceAll('_', ' ')
    .replace(/\b\w/g, (character) => character.toUpperCase())
}

export function frameRetryDelay(failures: number): number {
  return Math.min(10_000, 500 * (2 ** Math.min(Math.max(failures, 0), 5)))
}
