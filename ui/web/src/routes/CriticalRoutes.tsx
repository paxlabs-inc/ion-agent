import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import type { EventEnvelope, Operation } from '@matrixmcl/ion-shared'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useNavigate } from 'react-router-dom'
import { useOperator, useOperatorState } from '../app/operator-context'
import { Icon } from '../components/Icon'

export function CommandCenter() {
	const { client, connection, connectionError, sessionID } = useOperator()
  const state = useOperatorState()
  const checks = useQueries({
    queries: [
      ['system.health', {}],
      ['provider.list', {}],
      ['tool.readiness', {}],
      ['channel.health', {}],
	  ['work.brief', {}],
    ].map(([operation, payload]) => ({
	  queryKey: ['overview', operation, sessionID],
      queryFn: async () => {
        const response = await client.query<unknown>(
		  operation as Operation,
		  payload,
		  sessionID === undefined ? {} : { session_id: sessionID },
        )
        if (response.error !== undefined) throw new Error(response.error.message)
        return response.result
      },
      retry: 1,
	  refetchInterval: operation === 'work.brief' ? 5000 : false,
    })),
  })
  const securityEvents = state.recent_events.filter(
    (event) => event.type === 'security.alert' || event.type === 'circuit.opened',
  )
  const activeTurns = Object.values(state.turns).filter((turn) => turn.status === 'running')
  const health = record(checks[0]?.data)
  const providers = Array.isArray(checks[1]?.data) ? checks[1].data : []
  const provider = record(providers[0])
  const tools = record(checks[2]?.data)
  const channels = record(checks[3]?.data)
	const workBrief = record(checks[4]?.data)
	const contract = record(workBrief?.contract)
  const readyTools = Number(tools?.ready ?? 0)
  const configuredChannels = Number(channels?.configured ?? 0)
  const pendingApprovals = Object.keys(state.pending_approvals).length
  return (
    <div className="route-page">
      <header className="route-header">
        <div>
          <p className="eyebrow">OVERVIEW</p>
          <h1>Workspace status</h1>
          <p>Current work, decisions that need you, and system readiness.</p>
        </div>
        <Link className="primary-link" to="/chat">
          Open chat
        </Link>
      </header>
      {connectionError === undefined ? null : (
        <div className="callout danger" role="alert">
          <strong>Connection degraded</strong>
          <span>{connectionError}</span>
        </div>
      )}
      <section className="attention-strip" aria-label="Current work">
        <Metric
          label="Working now"
          value={String(activeTurns.length)}
          detail={activeTurns.length === 0 ? 'No request is running' : 'Request in progress'}
        />
        <Metric
          label="Needs your decision"
          value={String(pendingApprovals)}
          detail={pendingApprovals === 0 ? 'Nothing is waiting' : 'Review before action continues'}
        />
        <Metric
          label="Safety issues"
          value={String(securityEvents.length)}
          detail={securityEvents.length === 0 ? 'No recent alerts' : 'Review recent alerts'}
        />
      </section>
      <section className="command-center-grid" aria-label="Workspace readiness">
		<StatusCard
		  detail={
			contract === undefined
			  ? 'Set a concrete deliverable and proof of completion before substantial work begins.'
			  : `${String(workBrief?.next_action ?? 'Choose the next action')} · ${Number(workBrief?.completion_percentage ?? 0)}% verified`
		  }
		  label={contract === undefined ? 'What should happen next' : String(contract.goal ?? 'Current outcome')}
		  status={typeof workBrief?.blocking_reason === 'string' && workBrief.blocking_reason !== '' ? 'Waiting' : contract === undefined ? 'Not set' : 'In progress'}
		  to="/work"
		/>
        <StatusCard
          detail={
            checks[0]?.isError
              ? 'Ion did not answer its health check.'
              : health?.status === 'ready'
                ? 'Encrypted sessions and live updates are available.'
                : 'Checking the running service.'
          }
          label="Agent service"
          status={checks[0]?.isError ? 'Needs attention' : health?.status === 'ready' ? 'Ready' : 'Checking'}
          to="/diagnostics"
        />
        <StatusCard
          detail={
            provider === undefined
              ? 'Connect a model before starting real agent work.'
              : `${String(provider.name ?? 'Model')} · ${String(provider.model ?? 'model selected')}`
          }
          label="Model connection"
          status={provider === undefined ? 'Setup needed' : 'Configured'}
          to="/extensions"
        />
        <StatusCard
          detail={
            tools === undefined
              ? 'Checking available actions.'
              : `${readyTools} ready · ${Number(tools.unavailable ?? 0)} need setup`
          }
          label="Actions Ion can take"
          status={checks[2]?.isError ? 'Needs attention' : tools === undefined ? 'Checking' : readyTools > 0 ? 'Ready' : 'Setup needed'}
          to="/execution"
        />
        <StatusCard
          detail={
            channels === undefined
              ? 'Checking dashboard and external channels.'
              : `${Number(channels.healthy ?? 0)} of ${configuredChannels} configured channels healthy`
          }
          label="Availability"
          status={checks[3]?.isError ? 'Needs attention' : channels === undefined ? 'Checking' : configuredChannels > 0 ? 'Ready' : 'Setup needed'}
          to="/presence"
        />
      </section>
    </div>
  )
}

export function SessionsPage() {
  const operator = useOperator()
  const operatorState = useOperatorState()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [notice, setNotice] = useState<string>()
  const [search, setSearch] = useState('')
  const [view, setView] = useState<'active' | 'archived'>('active')
  const [editingID, setEditingID] = useState<string>()
  const [renameDraft, setRenameDraft] = useState('')
  const [deleteConfirmID, setDeleteConfirmID] = useState<string>()
  const [pendingID, setPendingID] = useState<string>()
  const sessions = useQuery({
    queryKey: ['session.list', view],
    queryFn: async () => {
      const response = await operator.client.query<
        Array<{
          id: string
          parent_id?: string
          title: string
          preview?: string
          created_at: string
          updated_at: string
        }>
      >('session.list', { archived: view === 'archived' })
      if (response.error !== undefined) throw new Error(response.error.message)
      return response.result ?? []
    },
    retry: false,
  })
  const filtered = (sessions.data ?? []).filter((session) =>
    `${session.title} ${session.preview ?? ''}`
      .toLowerCase()
      .includes(search.trim().toLowerCase()),
  )
  const create = async () => {
    const response = await operator.command<{ id: string }>(
      'session.create',
      {},
      crypto.randomUUID(),
    )
    if (response.error !== undefined || response.result?.id === undefined) {
      setNotice(response.error?.message ?? 'Session creation failed')
      return
    }
    operator.setSessionID(response.result.id)
    await queryClient.invalidateQueries({ queryKey: ['session.list'] })
    navigate('/chat')
  }
  const branch = async () => {
    if (operator.sessionID === undefined) return
    const response = await operator.command<{ id: string }>(
      'session.branch',
      {},
      crypto.randomUUID(),
      { session_id: operator.sessionID },
    )
    if (response.error !== undefined || response.result?.id === undefined) {
      setNotice(response.error?.message ?? 'Session branch failed')
      return
    }
    operator.setSessionID(response.result.id)
    await queryClient.invalidateQueries({ queryKey: ['session.list'] })
    navigate('/chat')
  }
  const rename = async (sessionID: string) => {
    const title = renameDraft.trim()
    if (title === '') return
    setPendingID(sessionID)
    setNotice(undefined)
    const response = await operator.command(
      'session.rename',
      { title },
      crypto.randomUUID(),
      { session_id: sessionID },
    )
    setPendingID(undefined)
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    setEditingID(undefined)
    setRenameDraft('')
    await queryClient.invalidateQueries({ queryKey: ['session.list'] })
  }
  const archive = async (sessionID: string, archived: boolean) => {
    setPendingID(sessionID)
    setNotice(undefined)
    const response = await operator.command(
      'session.archive',
      { archived },
      crypto.randomUUID(),
      { session_id: sessionID },
    )
    setPendingID(undefined)
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    if (archived && operator.sessionID === sessionID) {
      operator.setSessionID(undefined)
    }
    await queryClient.invalidateQueries({ queryKey: ['session.list'] })
  }
  const remove = async (sessionID: string) => {
    const running = Object.values(operatorState.turns).some((turn) =>
      turn.session_id === sessionID &&
      (turn.status === 'running' || turn.status === 'recovering')
    )
    if (running) {
      setNotice('Stop the active turn before deleting this conversation.')
      return
    }
    setPendingID(sessionID)
    setNotice(undefined)
    const response = await operator.command(
      'session.delete',
      { confirm_session_id: sessionID },
      crypto.randomUUID(),
      { session_id: sessionID },
    )
    setPendingID(undefined)
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    if (operator.sessionID === sessionID) {
      operator.setSessionID(undefined)
    }
    setDeleteConfirmID(undefined)
    await queryClient.invalidateQueries({ queryKey: ['session.list'] })
  }
  return (
    <div className="route-page conversations-page">
      <header className="route-header">
        <div>
          <p className="eyebrow">CONVERSATIONS</p>
          <h1>Your conversations</h1>
          <p>Pick up where you left off or start with a clean slate.</p>
        </div>
        <button onClick={() => void create()} type="button">
          <Icon name="plus" /> New conversation
        </button>
      </header>
      <section className="conversation-library">
        <div aria-label="Conversation view" className="conversation-view-tabs" role="group">
          <button
            aria-pressed={view === 'active'}
            onClick={() => setView('active')}
            type="button"
          >
            Active
          </button>
          <button
            aria-pressed={view === 'archived'}
            onClick={() => setView('archived')}
            type="button"
          >
            Archived
          </button>
        </div>
        <label className="conversation-search">
          <span className="sr-only">Search conversations</span>
          <Icon name="search" />
          <input
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search conversations"
            type="search"
            value={search}
          />
        </label>
        {sessions.isPending ? (
          <div className="library-empty">Loading your conversations…</div>
        ) : sessions.isError ? (
          <div className="library-empty danger">
            <strong>Conversations are unavailable</strong>
            <span>{sessions.error.message}</span>
          </div>
        ) : filtered.length === 0 ? (
          <div className="library-empty">
            <span className="welcome-mark"><Icon name="history" /></span>
            <strong>{search === '' ? 'No conversations yet' : 'No conversations found'}</strong>
            <span>
              {search === ''
                ? view === 'archived'
                  ? 'Archived conversations will appear here.'
                  : 'Start a conversation and it will appear here automatically.'
                : 'Try a different search.'}
            </span>
          </div>
        ) : (
          <div className="conversation-list">
            {filtered.map((session) => (
              <article
                className={operator.sessionID === session.id ? 'selected' : ''}
                key={session.id}
              >
                {editingID === session.id ? (
                  <form
                    className="conversation-rename"
                    onSubmit={(event) => {
                      event.preventDefault()
                      void rename(session.id)
                    }}
                  >
                    <label>
                      <span className="sr-only">Conversation name</span>
                      <input
                        autoFocus
                        maxLength={120}
                        onChange={(event) => setRenameDraft(event.target.value)}
                        value={renameDraft}
                      />
                    </label>
                    <button disabled={pendingID === session.id} type="submit">Save</button>
                    <button
                      className="quiet-button"
                      onClick={() => setEditingID(undefined)}
                      type="button"
                    >
                      Cancel
                    </button>
                  </form>
                ) : (
                  <>
                    <button
                      className="conversation-open"
                      onClick={() => {
                        operator.setSessionID(session.id)
                        navigate('/chat')
                      }}
                      type="button"
                    >
                      <span className="conversation-list-icon"><Icon name="spark" /></span>
                      <span className="conversation-list-copy">
                        <strong>{session.title}</strong>
                        <small>{session.preview ?? 'New conversation'}</small>
                      </span>
                      <time dateTime={session.updated_at}>{relativeTime(session.updated_at)}</time>
                    </button>
                    <div className="conversation-row-actions">
                      <button
                        onClick={() => {
                          setEditingID(session.id)
                          setRenameDraft(session.title)
                          setDeleteConfirmID(undefined)
                        }}
                        type="button"
                      >
                        <Icon name="edit" /> Rename
                      </button>
                      <button
                        onClick={() => void archive(session.id, view === 'active')}
                        type="button"
                      >
                        <Icon name="archive" />
                        {view === 'active' ? 'Archive' : 'Restore'}
                      </button>
                      <button
                        className="danger-action"
                        onClick={() => setDeleteConfirmID(
                          deleteConfirmID === session.id ? undefined : session.id,
                        )}
                        type="button"
                      >
                        <Icon name="trash" /> Delete
                      </button>
                    </div>
                    {deleteConfirmID === session.id ? (
                      <div className="conversation-delete-confirm">
                        <span>This permanently deletes only this conversation.</span>
                        <button
                          className="danger-button"
                          disabled={pendingID === session.id}
                          onClick={() => void remove(session.id)}
                          type="button"
                        >
                          Delete permanently
                        </button>
                        <button
                          className="quiet-button"
                          onClick={() => setDeleteConfirmID(undefined)}
                          type="button"
                        >
                          Cancel
                        </button>
                      </div>
                    ) : null}
                  </>
                )}
              </article>
            ))}
          </div>
        )}
      </section>
      {operator.sessionID === undefined ? null : (
        <div className="selected-conversation-actions">
          <span>Want to explore a different direction without changing the original?</span>
          <button className="quiet-button" onClick={() => void branch()} type="button">
            Branch selected conversation
          </button>
        </div>
      )}
      {notice === undefined ? null : <p className="surface-notice" role="status">{notice}</p>}
    </div>
  )
}

export function SecurityPage() {
  const state = useOperatorState()
  const events = state.recent_events.filter(
    (event) =>
      event.type.startsWith('policy.') ||
      event.type.startsWith('security.') ||
      event.type.startsWith('circuit.') ||
      event.type.startsWith('approval.'),
  )
  return (
    <EventPage
      eyebrow="SECURITY"
      title="Decisions before consequences"
      description="Review important safety decisions, approvals, and alerts without digging through chat."
      events={events}
    />
  )
}

export function IntegrityPage() {
  const state = useOperatorState()
  const events = state.recent_events.filter(
    (event) =>
      event.type === 'integrity.generated' ||
      event.type.startsWith('memory.') ||
      event.type.startsWith('premise.'),
  )
  return (
    <EventPage
      eyebrow="INTEGRITY"
      title="Can this information be trusted?"
      description="Review where saved knowledge came from and whether important records changed unexpectedly."
      events={events}
    />
  )
}

function EventPage({
  eyebrow,
  title,
  description,
  events,
}: {
  eyebrow: string
  title: string
  description: string
  events: EventEnvelope[]
}) {
  return (
    <div className="route-page">
      <header className="route-header">
        <div>
          <p className="eyebrow">{eyebrow}</p>
          <h1>{title}</h1>
          <p>{description}</p>
        </div>
      </header>
      <div className="event-table" role="table" aria-label={`${eyebrow} events`}>
        <div className="event-row event-row-head" role="row">
          <span role="columnheader">When</span>
          <span role="columnheader">What happened</span>
          <span role="columnheader">Related work</span>
          <span role="columnheader">More</span>
        </div>
        {events.length === 0 ? (
          <p className="empty-state">Nothing needs your attention here yet.</p>
        ) : (
          events
            .slice()
            .reverse()
            .map((event) => (
              <div className="event-row" key={event.event_id} role="row">
                <span role="cell" title={`Event ${String(event.sequence)}`}>
                  {formatTimestamp(event.occurred_at)}
                </span>
                <strong role="cell">{eventTitle(event.type)}</strong>
                <code role="cell">
                  {event.correlation.turn_id?.slice(0, 8) ??
                    event.correlation.session_id?.slice(0, 8) ??
                    'System-wide'}
                </code>
                <details role="cell">
                  <summary>Technical details</summary>
                  <code>{event.type}: {eventPayload(event)}</code>
                </details>
              </div>
            ))
        )}
      </div>
    </div>
  )
}

function Metric({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <article className="metric-card">
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  )
}

function StatusCard({
  detail,
  label,
  status,
  to,
}: {
  detail: string
  label: string
  status: string
  to: string
}) {
  const attention = status === 'Needs attention' || status === 'Setup needed'
  return (
    <Link className="status-card" data-attention={attention} to={to}>
      <span>{label}</span>
      <strong>{status}</strong>
      <small>{detail}</small>
      <b>Open →</b>
    </Link>
  )
}

function record(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

function compact(value: unknown): string {
  const encoded = JSON.stringify(value)
  return encoded.length > 160 ? `${encoded.slice(0, 157)}…` : encoded
}

function eventPayload(event: EventEnvelope): string {
  if (event.type.startsWith('approval.')) return JSON.stringify(event.payload)
  return compact(event.payload)
}

function eventTitle(type: string): string {
  const labels: Record<string, string> = {
    'approval.requested': 'A decision is waiting for you',
    'approval.responded': 'Your decision was recorded',
    'approval.resolved': 'Your decision was recorded',
    'circuit.opened': 'A safety limit stopped an action',
    'integrity.generated': 'A safety check finished',
    'memory.created': 'Knowledge was saved',
    'memory.deleted': 'Saved knowledge was removed',
    'memory.updated': 'Saved knowledge changed',
    'policy.allowed': 'An action passed its safety checks',
    'policy.decision': 'A safety decision was recorded',
    'policy.denied': 'An action was blocked by a safety rule',
    'premise.created': 'An assumption was recorded',
    'premise.updated': 'An assumption changed',
    'security.alert': 'A security issue needs attention',
  }
  return labels[type] ?? type.split('.').map(humanize).join(' · ')
}

function humanize(value: string): string {
  const spaced = value.replaceAll('_', ' ')
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

function formatTimestamp(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleTimeString()
}

function relativeTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return value
  const elapsed = Date.now() - date.valueOf()
  if (elapsed < 60_000) return 'Just now'
  if (elapsed < 3_600_000) return `${String(Math.floor(elapsed / 60_000))}m ago`
  if (elapsed < 86_400_000) return `${String(Math.floor(elapsed / 3_600_000))}h ago`
  if (elapsed < 604_800_000) return `${String(Math.floor(elapsed / 86_400_000))}d ago`
  return date.toLocaleDateString([], { month: 'short', day: 'numeric' })
}
