import {
  displayModelCompatibility,
  isComputerEventPayload,
  migrateDisplayModel,
  type EventEnvelope,
} from '@matrixmcl/ion-shared'
import { useQuery } from '@tanstack/react-query'
import {
  type KeyboardEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useOperator, useOperatorState } from '../../app/operator-context'
import { Icon } from '../../components/Icon'
import { NativeApplication } from './NativeApplications'
import { PrivateDesktop } from './PrivateDesktop'

const timelineWindow = 200

type ComputerView = 'watch' | 'explore'

interface AgentProjection {
  id: string
  parent_id?: string
  depth: number
  state: string
  assignment?: string
  created_at: string
  last_verb_at: string
  last_verb?: string
  artifact_count: number
  has_error: boolean
}

interface SwarmProjection {
  status: string
  active: number
  global_limit: number
  session_limit: number
  parent_limit: number
  agents?: AgentProjection[]
}

interface ComputerControlLease {
  protocol_version: string
  lease_id?: string
  target: {
    actor_id: string
    session_id?: string
    resource_kind: 'browser' | 'terminal'
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

interface BrowserSnapshot {
  url: string
  title: string
  text: string
  elements: Array<{
    ref: string
    tag: string
    text?: string
    name?: string
    placeholder?: string
    disabled?: boolean
  }>
}

interface BrowserWorkflow {
  id: string
  status: 'active' | 'paused' | 'waiting_for_human' | 'cancelled' | 'restart_required'
  origin: string
  revision: number
  preview: BrowserSnapshot
  handoff?: {
    kind: string
    consequence: string
    requested_at: string
  }
  reason?: string
  updated_at: string
}

export function ComputerStage({
  active = true,
  onClose,
}: {
  active?: boolean
  onClose?: () => void
}) {
  const operator = useOperator()
  const state = useOperatorState()
  const [workspace, setWorkspace] = useState('aggregate')
  const [positions, setPositions] = useState<Record<string, {
    follow: boolean
    eventID?: string
  }>>({})
  const [view, setView] = useState<ComputerView>('watch')
  const [fullScreen, setFullScreen] = useState(false)
  const [controlNotice, setControlNotice] = useState<string>()
  const [browserURL, setBrowserURL] = useState('')
  const [browserRef, setBrowserRef] = useState('')
  const [browserValue, setBrowserValue] = useState('')
  const [browserSnapshot, setBrowserSnapshot] = useState<BrowserSnapshot>()
  const [handoffKind, setHandoffKind] = useState('captcha')
  const [handoffConsequence, setHandoffConsequence] = useState('')
  const [credentialOrigin, setCredentialOrigin] = useState('')
  const [credentialLabel, setCredentialLabel] = useState('')
  const [credentialSecret, setCredentialSecret] = useState('')
  const selectedRef = useRef<HTMLButtonElement>(null)
  const swarm = useQuery({
    queryKey: ['computer-agent-workspaces', operator.sessionID],
    enabled: active && operator.sessionID !== undefined,
    refetchInterval: active ? 2_000 : false,
    queryFn: async () => {
      const response = await operator.client.query<SwarmProjection>(
        'swarm.list',
        {},
        operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
      )
      if (response.error !== undefined) throw new Error(response.error.message)
      return response.result
    },
  })
  const agentRecords = swarm.data?.agents ?? []
  const browserWorkflows = useQuery({
    queryKey: ['browser-workflows', operator.sessionID],
    enabled: active && operator.sessionID !== undefined,
    refetchInterval: active ? 2_000 : false,
    queryFn: async () => {
      const response = await operator.client.query<BrowserWorkflow[]>(
        'browser.workflow.list',
        {},
        operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
      )
      if (response.error !== undefined) throw new Error(response.error.message)
      return Array.isArray(response.result) ? response.result : []
    },
  })

  const scopedEvents = useMemo(
    () => state.recent_events.filter((event) => {
      if (!event.type.startsWith('tool.')) return false
      return operator.sessionID === undefined ||
        event.correlation.session_id === operator.sessionID
    }),
    [operator.sessionID, state.recent_events],
  )
  const agents = useMemo(() => {
    const found = new Set<string>()
    for (const event of scopedEvents) {
      if (isComputerEventPayload(event.payload)) {
        found.add(event.payload.scope.agent_id)
      }
    }
    for (const agent of agentRecords) found.add(agent.id)
    return [...found].sort((left, right) => left.localeCompare(right))
  }, [agentRecords, scopedEvents])
  const events = workspace === 'aggregate'
    ? scopedEvents
    : scopedEvents.filter((event) =>
        isComputerEventPayload(event.payload) &&
        event.payload.scope.agent_id === workspace
      )
  const position = positions[workspace]
  const followLive = position?.follow ?? true
  const selectedEventID = position?.eventID
  const latest = events.at(-1)
  const selected = followLive
    ? latest
    : events.find((event) => event.event_id === selectedEventID) ?? latest
  const selectedIndex = selected === undefined
    ? -1
    : events.findIndex((event) => event.event_id === selected.event_id)
  const windowStart = Math.max(
    0,
    Math.min(
      Math.max(0, events.length - timelineWindow),
      selectedIndex < 0 ? 0 : selectedIndex - Math.floor(timelineWindow / 2),
    ),
  )
  const visibleEvents = events.slice(windowStart, windowStart + timelineWindow)
  const hiddenBefore = windowStart
  const hiddenAfter = Math.max(0, events.length - windowStart - visibleEvents.length)

  useEffect(() => {
    if (!fullScreen) return
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') setFullScreen(false)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [fullScreen])

  const selectEvent = (event: EventEnvelope) => {
    setPositions((current) => ({
      ...current,
      [workspace]: {
        eventID: event.event_id,
        follow: event.event_id === latest?.event_id,
      },
    }))
  }
  const backToLive = () => {
    setPositions((current) => ({
      ...current,
      [workspace]: { follow: true },
    }))
  }
  const moveSelection = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    let next = index
    if (event.key === 'ArrowUp') next = Math.max(0, index - 1)
    else if (event.key === 'ArrowDown') next = Math.min(events.length - 1, index + 1)
    else if (event.key === 'Home') next = 0
    else if (event.key === 'End') next = events.length - 1
    else return
    event.preventDefault()
    const nextEvent = events[next]
    if (nextEvent === undefined) return
    selectEvent(nextEvent)
    requestAnimationFrame(() => selectedRef.current?.focus())
  }

  const payload = selected !== undefined && isComputerEventPayload(selected.payload)
    ? selected.payload
    : undefined
  const compatibility = payload?.display_model === undefined
    ? undefined
    : displayModelCompatibility(
        payload.display_model,
        payload.source_references.length,
      )
  const display = payload !== undefined &&
      (compatibility === 'current' || compatibility === 'migrated')
    ? migrateDisplayModel(payload.display_model, payload.source_references.length)
    : undefined
  const status = stageStatus(selected, payload, state.gap, operator.connection)
  const selectedAgent = workspace === 'aggregate'
    ? undefined
    : agentRecords.find((agent) => agent.id === workspace)
  const browserControlTarget = payload !== undefined &&
      payload.tool.startsWith('browser_') &&
      operator.sessionID !== undefined
    ? {
        resource_kind: 'browser' as const,
        resource_id: operator.sessionID,
        target_revision: selected?.sequence ?? 0,
      }
    : undefined
  const control = useQuery({
    queryKey: [
      'computer-control',
      operator.sessionID,
      browserControlTarget?.resource_kind,
      browserControlTarget?.resource_id,
      browserControlTarget?.target_revision,
    ],
    enabled: active && browserControlTarget !== undefined,
    refetchInterval: active && browserControlTarget !== undefined ? 1_000 : false,
    queryFn: async () => {
      if (browserControlTarget === undefined) throw new Error('No control target')
      const response = await operator.client.query<ComputerControlLease>(
        'computer.control.get',
        browserControlTarget,
        operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
      )
      if (response.error !== undefined) throw new Error(response.error.message)
      if (response.result === undefined) throw new Error('Control authority is unavailable')
      return response.result
    },
  })

  const controlCommand = async (
    operation: 'computer.control.acquire' | 'computer.control.renew' | 'computer.control.release',
  ) => {
    const current = control.data
    if (browserControlTarget === undefined || current === undefined) return
    const payload = operation === 'computer.control.acquire'
      ? {
          ...browserControlTarget,
          owner: current.owner,
          expected_lease_revision: current.revision,
          ttl_seconds: 90,
        }
      : {
          resource_kind: browserControlTarget.resource_kind,
          resource_id: browserControlTarget.resource_id,
          lease_id: current.lease_id,
          expected_lease_revision: current.revision,
          ...(operation === 'computer.control.renew' ? { ttl_seconds: 90 } : {}),
        }
    const response = await operator.command<ComputerControlLease>(
      operation,
      payload,
      crypto.randomUUID(),
      operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
    )
    setControlNotice(response.error?.message ?? (
      operation === 'computer.control.acquire'
        ? 'Browser control acquired. Automation is paused at the action boundary.'
        : operation === 'computer.control.renew'
          ? 'Browser control renewed.'
          : 'Browser control returned to the executor.'
    ))
    await control.refetch()
  }

  const browserControl = async (
    operation:
      | 'computer.browser.observe'
      | 'computer.browser.navigate'
      | 'computer.browser.interact'
      | 'computer.browser.submit',
    extra: Record<string, unknown> = {},
  ) => {
    const current = control.data
    if (
      browserControlTarget === undefined ||
      current?.lease_id === undefined ||
      current.state !== 'active'
    ) return
    const payload = {
      resource_kind: browserControlTarget.resource_kind,
      resource_id: browserControlTarget.resource_id,
      lease_id: current.lease_id,
      expected_lease_revision: current.revision,
      ...extra,
    }
    const response = operation === 'computer.browser.observe'
      ? await operator.client.query<BrowserSnapshot>(
          operation,
          payload,
          operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
        )
      : await operator.command<BrowserSnapshot>(
          operation,
          payload,
          crypto.randomUUID(),
          operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
        )
    if (response.error !== undefined || response.result === undefined) {
      setControlNotice(response.error?.message ?? 'Browser control action failed.')
      return
    }
    setBrowserSnapshot(response.result)
    setControlNotice('Browser state reconciled from the controlled executor.')
  }
  const workflowCommand = async (
    operation:
      | 'browser.workflow.pause'
      | 'browser.workflow.resume'
      | 'browser.workflow.cancel'
      | 'browser.workflow.handoff',
    workflowID: string,
  ) => {
    const payload = operation === 'browser.workflow.handoff'
      ? {
          workflow_id: workflowID,
          kind: handoffKind,
          consequence: handoffConsequence,
        }
      : { workflow_id: workflowID }
    const response = await operator.command(
      operation,
      payload,
      crypto.randomUUID(),
      operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
    )
    setControlNotice(response.error?.message ?? 'Browser workflow updated.')
    await browserWorkflows.refetch()
  }
  const saveCredential = async () => {
    if (operator.sessionID === undefined) return
    const response = await fetch(
      `/v1/browser-credentials?session_id=${encodeURIComponent(operator.sessionID)}`,
      {
        method: 'POST',
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
          'X-Ion-CSRF': readCookie('__Host-ion_csrf'),
        },
        body: JSON.stringify({
          origin: credentialOrigin,
          label: credentialLabel,
          secret: credentialSecret,
        }),
      },
    )
    setCredentialSecret('')
    setControlNotice(response.ok
      ? 'Credential stored as an origin-bound reference. Its value will not be shown again.'
      : 'Credential was rejected.')
  }

  return (
    <section
      aria-label="Computer"
      aria-live="polite"
      className="computer-stage"
      data-active={active}
      data-fullscreen={fullScreen}
      data-status={status.tone}
      data-testid="computer-stage"
    >
      <header className="computer-stage-header">
        <div className="computer-stage-heading">
          <p className="eyebrow">Computer</p>
          <h2>{display?.title.value ?? activityTitle(payload)}</h2>
          <p>{status.label}</p>
        </div>
        <div className="computer-stage-actions">
          <div aria-label="Computer view" className="computer-view-switch" role="group">
            <button
              aria-pressed={view === 'watch'}
              onClick={() => setView('watch')}
              type="button"
            >
              Watch
            </button>
            <button
              aria-pressed={view === 'explore'}
              onClick={() => setView('explore')}
              type="button"
            >
              Explore
            </button>
          </div>
          <button
            aria-label={fullScreen ? 'Exit full screen' : 'Open Computer full screen'}
            aria-pressed={fullScreen}
            className="icon-button"
            onClick={() => setFullScreen((current) => !current)}
            type="button"
          >
            <Icon name={fullScreen ? 'panel-left-open' : 'panel-left-close'} />
          </button>
          {onClose === undefined ? null : (
            <button
              aria-label="Close Computer"
              className="icon-button"
              onClick={onClose}
              type="button"
            >
              <Icon name="close" />
            </button>
          )}
        </div>
        <dl className="computer-stage-context">
          <ContextItem
            label="Task"
            value={payload?.scope.task_id ?? payload?.scope.outcome_id ?? selectedAgent?.assignment}
          />
          <ContextItem label="Agent" value={payload?.scope.agent_id ?? selectedAgent?.id} />
          <ContextItem label="App" value={payload?.display_kind} />
          <ContextItem label="Action" value={payload?.operation} />
          <ContextItem label="Status" value={payload?.phase ?? status.label} />
          <ContextItem label="Latest event" value={formatTimestamp(latest?.occurred_at)} />
        </dl>
        <div className="computer-agent-workspaces">
          <span>Agent workspace</span>
          <div aria-label="Agent workspaces" role="group">
            <button
              aria-pressed={workspace === 'aggregate'}
              onClick={() => setWorkspace('aggregate')}
              type="button"
            >
              All agents
              <small>{String(agents.length)}</small>
            </button>
            {agents.map((agent) => {
              const record = agentRecords.find((candidate) => candidate.id === agent)
              const latestForAgent = [...scopedEvents].reverse().find((event) =>
                isComputerEventPayload(event.payload) &&
                event.payload.scope.agent_id === agent
              )
              const running = record?.state === 'running' || (
                latestForAgent !== undefined &&
                isComputerEventPayload(latestForAgent.payload) &&
                !['completed', 'failed', 'denied', 'interrupted', 'outcome_unknown']
                  .includes(latestForAgent.payload.phase)
              )
              return (
                <button
                  aria-pressed={workspace === agent}
                  key={agent}
                  onClick={() => setWorkspace(agent)}
                  type="button"
                >
                  {humanize(agent)}
                  <small>{running ? 'working in background' : 'retained'}</small>
                </button>
              )
            })}
          </div>
          <p>
            {workspace === 'aggregate'
              ? 'Aggregate events retain durable sequence order.'
              : `Inspecting ${humanize(workspace)}. Other agents continue in the background.`}
          </p>
        </div>
      </header>

      <div className="computer-stage-body">
        <main className="computer-canvas" aria-label="Computer activity">
          <PrivateDesktop active={active} />
          {selectedAgent === undefined ? null : (
            <section className="computer-agent-summary" aria-label="Selected agent assignment">
              <div>
                <span>Assignment</span>
                <strong>{selectedAgent.assignment ?? 'No assignment summary available'}</strong>
              </div>
              <dl>
                <ContextItem label="State" value={selectedAgent.state} />
                <ContextItem label="Parent" value={selectedAgent.parent_id} />
                <ContextItem label="Depth" value={String(selectedAgent.depth)} />
                <ContextItem
                  label="Artifacts"
                  value={String(selectedAgent.artifact_count)}
                />
              </dl>
            </section>
          )}
          {selected === undefined ? (
            <StageEmpty connection={operator.connection} />
          ) : payload === undefined ? (
            <StageBoundary
              title="This retained activity cannot be displayed"
              detail="Its lifecycle version is not supported. Execution state has not been inferred."
            />
          ) : compatibility === 'unsupported' ? (
            <StageBoundary
              title="This display version is not supported"
              detail="The action and lifecycle remain visible, but this client will not guess at its result."
            />
          ) : display === undefined ? (
            <StageLifecycle event={selected} />
          ) : (
            <NativeApplication
              display={display}
              event={selected}
              migrated={compatibility === 'migrated'}
              sources={payload.source_references}
            />
          )}
          {state.gap ? (
            <div className="computer-retention-note" role="status">
              Earlier activity is outside the retained event window. The visible history is complete
              from the recovery marker forward.
            </div>
          ) : null}
          {operator.sessionID === undefined ? null : (
            <section className="computer-control" aria-label="Supervised browser workflows">
              <header>
                <div>
                  <span>Browser workflows</span>
                  <strong>{browserWorkflows.data?.length ?? 0}</strong>
                </div>
                <div>
                  <span>Persistence</span>
                  <strong>Metadata only</strong>
                </div>
                <div>
                  <span>Browser profiles</span>
                  <strong>Volatile</strong>
                </div>
              </header>
              {browserWorkflows.error === null || browserWorkflows.error === undefined ? null : (
                <p role="alert">{browserWorkflows.error instanceof Error
                  ? browserWorkflows.error.message
                  : String(browserWorkflows.error)}</p>
              )}
              {(browserWorkflows.data ?? []).map((workflow) => (
                <article key={workflow.id}>
                  <h3>{workflow.preview.title || workflow.origin}</h3>
                  <p>{humanize(workflow.status)} · revision {workflow.revision}</p>
                  {workflow.handoff === undefined ? null : (
                    <p>{humanize(workflow.handoff.kind)}: {workflow.handoff.consequence}</p>
                  )}
                  {workflow.reason === undefined ? null : <p>{workflow.reason}</p>}
                  <div className="computer-control-actions">
                    {workflow.status === 'active' ? (
                      <>
                        <button onClick={() => { void workflowCommand('browser.workflow.pause', workflow.id) }} type="button">
                          Pause
                        </button>
                        <label>
                          Human handoff
                          <select onChange={(event) => setHandoffKind(event.target.value)} value={handoffKind}>
                            <option value="captcha">CAPTCHA</option>
                            <option value="passkey">Passkey</option>
                            <option value="legal_identity">Legal identity</option>
                            <option value="terms">Terms</option>
                            <option value="payment">Payment</option>
                            <option value="recovery">Recovery</option>
                            <option value="ambiguous_control">Ambiguous control</option>
                          </select>
                        </label>
                        <label>
                          Consequence
                          <input onChange={(event) => setHandoffConsequence(event.target.value)} value={handoffConsequence} />
                        </label>
                        <button
                          disabled={handoffConsequence.trim() === ''}
                          onClick={() => { void workflowCommand('browser.workflow.handoff', workflow.id) }}
                          type="button"
                        >
                          Request handoff
                        </button>
                      </>
                    ) : workflow.status === 'paused' || workflow.status === 'waiting_for_human' ? (
                      <button onClick={() => { void workflowCommand('browser.workflow.resume', workflow.id) }} type="button">
                        Resume
                      </button>
                    ) : null}
                    {workflow.status === 'cancelled' ? null : (
                      <button onClick={() => { void workflowCommand('browser.workflow.cancel', workflow.id) }} type="button">
                        Cancel and clear browser
                      </button>
                    )}
                  </div>
                </article>
              ))}
              <details>
                <summary>Store an origin-bound credential reference</summary>
                <label>
                  Origin
                  <input onChange={(event) => setCredentialOrigin(event.target.value)} placeholder="https://service.example" value={credentialOrigin} />
                </label>
                <label>
                  Label
                  <input onChange={(event) => setCredentialLabel(event.target.value)} value={credentialLabel} />
                </label>
                <label>
                  Private value
                  <input autoComplete="off" onChange={(event) => setCredentialSecret(event.target.value)} type="password" value={credentialSecret} />
                </label>
                <button
                  disabled={credentialOrigin.trim() === '' || credentialLabel.trim() === '' || credentialSecret === ''}
                  onClick={() => { void saveCredential() }}
                  type="button"
                >
                  Store private reference
                </button>
                <p>The value is write-only and is never returned in workflow state or events.</p>
              </details>
            </section>
          )}
          {browserControlTarget === undefined ? null : (
            <section className="computer-control" aria-label="Browser control authority">
              <header>
                <div>
                  <span>Control authority</span>
                  <strong>
                    {control.data?.authority === 'operator'
                      ? 'You have control'
                      : 'Ion has control'}
                  </strong>
                </div>
                <div>
                  <span>Lease expiry</span>
                  <strong>{formatTimestamp(control.data?.expires_at)}</strong>
                </div>
                <div>
                  <span>Reconciliation</span>
                  <strong>{humanize(control.data?.reconciliation ?? 'loading')}</strong>
                </div>
              </header>
              {control.error === null || control.error === undefined ? null : (
                <p role="alert">
                  {control.error instanceof Error
                    ? control.error.message
                    : String(control.error)}
                </p>
              )}
              <div className="computer-control-actions">
                {control.data?.state === 'active' ? (
                  <>
                    <button onClick={() => { void controlCommand('computer.control.renew') }} type="button">
                      Renew control
                    </button>
                    <button onClick={() => { void controlCommand('computer.control.release') }} type="button">
                      Return control
                    </button>
                  </>
                ) : (
                  <button
                    disabled={control.data === undefined || control.data.owner.revision === 0}
                    onClick={() => { void controlCommand('computer.control.acquire') }}
                    type="button"
                  >
                    Take control
                  </button>
                )}
              </div>
              {control.data?.state === 'active' ? (
                <div className="computer-browser-control">
                  <div>
                    <button onClick={() => { void browserControl('computer.browser.observe') }} type="button">
                      Refresh page state
                    </button>
                    <label>
                      Address
                      <input onChange={(event) => setBrowserURL(event.target.value)} value={browserURL} />
                    </label>
                    <button
                      disabled={browserURL.trim() === ''}
                      onClick={() => { void browserControl('computer.browser.navigate', { url: browserURL }) }}
                      type="button"
                    >
                      Navigate
                    </button>
                  </div>
                  <div>
                    <label>
                      Element reference
                      <input onChange={(event) => setBrowserRef(event.target.value)} placeholder="p1" value={browserRef} />
                    </label>
                    <label>
                      Fill value
                      <input onChange={(event) => setBrowserValue(event.target.value)} value={browserValue} />
                    </label>
                    <button
                      disabled={browserRef.trim() === ''}
                      onClick={() => { void browserControl('computer.browser.interact', { action: browserValue === '' ? 'click' : 'fill', ref: browserRef, value: browserValue }) }}
                      type="button"
                    >
                      {browserValue === '' ? 'Click element' : 'Fill element'}
                    </button>
                    <button
                      disabled={browserRef.trim() === '' || browserValue !== ''}
                      onClick={() => { void browserControl('computer.browser.submit', { ref: browserRef }) }}
                      type="button"
                    >
                      Activate consequential control
                    </button>
                  </div>
                  {browserSnapshot === undefined ? null : (
                    <article>
                      <h3>{browserSnapshot.title || browserSnapshot.url}</h3>
                      <p>{browserSnapshot.text}</p>
                      <ul>
                        {browserSnapshot.elements.slice(0, 24).map((element) => (
                          <li key={element.ref}>
                            <code>{element.ref}</code>
                            <span>{element.name ?? element.text ?? element.placeholder ?? element.tag}</span>
                          </li>
                        ))}
                      </ul>
                    </article>
                  )}
                </div>
              ) : null}
              {controlNotice === undefined ? null : <p role="status">{controlNotice}</p>}
            </section>
          )}
          {view === 'explore' && payload !== undefined ? (
            <aside className="computer-inspector" aria-label="Activity facts and sources">
              <h3>Activity facts</h3>
              <dl>
                <ContextItem label="Truth" value={display?.title.truth ?? 'lifecycle only'} />
                <ContextItem label="Risk" value={payload.risk_class} />
                <ContextItem label="Tool" value={payload.tool} />
                <ContextItem
                  label="Result"
                  value={payload.result === undefined
                    ? 'not terminal'
                    : payload.result.available
                      ? `${String(payload.result.bytes)} bytes available`
                      : payload.result.error ?? payload.result.error_code ?? 'unavailable'}
                />
              </dl>
              <h3>Sources</h3>
              <ul>
                {payload.source_references.map((source, index) => (
                  <li key={`${source.kind}-${source.id}`}>
                    <span>{source.kind.replaceAll('_', ' ')}</span>
                    <code title={source.id}>Source {String(index + 1)}</code>
                  </li>
                ))}
              </ul>
              <p>Exploring is read-only. Use explicit conversation controls to steer, retry, approve, or take over.</p>
            </aside>
          ) : null}
        </main>

        <aside className="computer-timeline" aria-label="Computer history">
          <div className="computer-timeline-header">
            <div>
              <h3>Timeline</h3>
              <p>{followLive ? 'Following live activity' : 'Viewing history while work continues'}</p>
            </div>
            {followLive ? (
              <button onClick={() => {
                setPositions((current) => ({
                  ...current,
                  [workspace]: {
                    follow: false,
                    ...(latest === undefined ? {} : { eventID: latest.event_id }),
                  },
                }))
              }} type="button">
                Pause view
              </button>
            ) : (
              <button onClick={backToLive} type="button">Back to live</button>
            )}
          </div>
          <div
            aria-label="Computer event timeline"
            className="computer-timeline-list"
            role="list"
          >
            {hiddenBefore > 0 ? (
              <p className="computer-window-note">{String(hiddenBefore)} earlier events not rendered</p>
            ) : null}
            {visibleEvents.map((event) => {
              const index = events.findIndex((item) => item.event_id === event.event_id)
              const active = selected?.event_id === event.event_id
              return (
                <div key={event.event_id} role="listitem">
                  <button
                    aria-current={active ? 'true' : undefined}
                    className="computer-timeline-event"
                    onClick={() => selectEvent(event)}
                    onKeyDown={(keyboardEvent) => moveSelection(keyboardEvent, index)}
                    ref={active ? selectedRef : undefined}
                    tabIndex={active ? 0 : -1}
                    type="button"
                  >
                    <span>{formatTimestamp(event.occurred_at)}</span>
                    <strong>{timelineTitle(event)}</strong>
                    <span>{timelinePhase(event)}</span>
                  </button>
                </div>
              )
            })}
            {events.length === 0 ? <p role="listitem">No Computer activity yet.</p> : null}
            {hiddenAfter > 0 ? (
              <p className="computer-window-note">{String(hiddenAfter)} newer events not rendered</p>
            ) : null}
          </div>
        </aside>
      </div>
    </section>
  )
}

function ContextItem({ label, value }: { label: string; value: string | undefined }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd title={value}>{value === undefined || value === '' ? '—' : humanize(value)}</dd>
    </div>
  )
}

function StageLifecycle({ event }: { event: EventEnvelope }) {
  const payload = isComputerEventPayload(event.payload) ? event.payload : undefined
  const detail = payload?.phase === 'awaiting_approval'
    ? 'This action is waiting for an explicit approval decision.'
    : payload?.phase === 'progress'
      ? 'The action reported progress. No completion is claimed yet.'
      : payload?.phase === 'outcome_unknown'
        ? 'Delivery may have happened, but Ion cannot verify the outcome.'
        : `Lifecycle state: ${humanize(payload?.phase ?? event.type)}.`
  return (
    <StageBoundary
      detail={detail}
      title={activityTitle(payload)}
    />
  )
}

function StageEmpty({ connection }: { connection: 'connecting' | 'ready' | 'degraded' }) {
  if (connection === 'connecting') {
    return (
      <StageBoundary
        detail="Recovering the durable event stream before showing activity."
        title="Connecting to Computer"
      />
    )
  }
  if (connection === 'degraded') {
    return (
      <StageBoundary
        detail="Live updates are temporarily unavailable. Retained history will return after reconnection."
        title="Computer connection interrupted"
      />
    )
  }
  return (
    <StageBoundary
      detail="Actions will appear here with their real status, evidence, and source attribution."
      title="No Computer activity yet"
    />
  )
}

function StageBoundary({ detail, title }: { detail: string; title: string }) {
  return (
    <div className="computer-stage-boundary">
      <Icon name="activity" />
      <div>
        <h3>{title}</h3>
        <p>{detail}</p>
      </div>
    </div>
  )
}

function stageStatus(
  event: EventEnvelope | undefined,
  payload: ReturnType<typeof computerPayload>,
  gap: boolean,
  connection: 'connecting' | 'ready' | 'degraded',
): { label: string; tone: string } {
  if (connection === 'connecting') return { label: 'Recovering live state', tone: 'recovering' }
  if (connection === 'degraded') return { label: 'Connection interrupted', tone: 'degraded' }
  if (gap) return { label: 'Live with a retention gap', tone: 'degraded' }
  if (event === undefined) return { label: 'Ready', tone: 'empty' }
  if (payload === undefined) return { label: 'Unsupported retained event', tone: 'unsupported' }
  if (payload.phase === 'awaiting_approval') return { label: 'Awaiting your decision', tone: 'waiting' }
  if (payload.phase === 'outcome_unknown') return { label: 'Outcome unknown', tone: 'degraded' }
  if (payload.phase === 'failed' || payload.phase === 'denied') {
    return { label: humanize(payload.phase), tone: 'failed' }
  }
  if (payload.phase === 'interrupted') return { label: 'Interrupted', tone: 'degraded' }
  if (payload.phase === 'completed') return { label: 'Completed', tone: 'completed' }
  return { label: humanize(payload.phase), tone: 'active' }
}

function computerPayload(event: EventEnvelope | undefined) {
  return event !== undefined && isComputerEventPayload(event.payload)
    ? event.payload
    : undefined
}

function activityTitle(payload: ReturnType<typeof computerPayload>): string {
  if (payload === undefined) return 'Computer workspace'
  return humanize(payload.tool)
}

function timelineTitle(event: EventEnvelope): string {
  const payload = computerPayload(event)
  return payload === undefined ? 'Unsupported activity' : humanize(payload.operation)
}

function timelinePhase(event: EventEnvelope): string {
  const payload = computerPayload(event)
  return humanize(payload?.phase ?? event.type)
}

function humanize(value: string): string {
  return value.replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase())
}

function formatTimestamp(value: string | undefined): string {
  if (value === undefined) return '—'
  const parsed = new Date(value)
  return Number.isNaN(parsed.valueOf())
    ? value
    : parsed.toLocaleTimeString([], {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
      })
}

function readCookie(name: string): string {
  if (typeof document === 'undefined') return ''
  const prefix = `${encodeURIComponent(name)}=`
  const found = document.cookie.split('; ').find((part) => part.startsWith(prefix))
  return found === undefined ? '' : decodeURIComponent(found.slice(prefix.length))
}
