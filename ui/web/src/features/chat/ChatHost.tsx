import {
  displayModelCompatibility,
  isComputerEventPayload,
  migrateDisplayModel,
  type EventEnvelope,
} from '@matrixmcl/ion-shared'
import { useQueryClient } from '@tanstack/react-query'
import {
  type FormEvent,
  type KeyboardEvent,
  useEffect,
  useRef,
  useState,
} from 'react'
import Markdown from 'react-markdown'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import remarkGfm from 'remark-gfm'
import { useOperator, useOperatorState } from '../../app/operator-context'
import { Icon } from '../../components/Icon'
import { conciseProjectName } from '../../lib/project-name'

interface SessionResult {
  id: string
}

interface TurnResult {
  turn_id: string
  state: string
}

interface StudioProjectResult {
  id: string
  name: string
}

interface QueuedSubmission {
  content: string
  mode: 'full' | 'studio'
  pathname: string
}

interface ResumedSession {
  messages: Array<{
    id: string
    role: 'system' | 'user' | 'assistant' | 'tool'
    memory_type?: 'transcript' | 'summary' | 'tool-event'
    turn_id?: string
    content: string
    created_at: string
  }>
}

export interface DisplayMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  createdAt?: string
  turnID?: string
}

const starters = [
  {
    title: 'Research something',
    prompt: 'Research this topic thoroughly, compare reliable sources, and give me a clear recommendation: ',
  },
  {
    title: 'Plan a project',
    prompt: 'Help me turn this goal into a practical plan, then work through it with me: ',
  },
  {
    title: 'Create something',
    prompt: 'Help me create a polished first version of: ',
  },
  {
    title: 'Make sense of information',
    prompt: 'Analyze this information, explain what matters, and tell me what I should do next: ',
  },
] as const

export function ChatHost({
  active,
  computerReviewAvailable = false,
  computerReviewOpen = false,
  mode = 'full',
  onComputerReviewToggle,
}: {
  active: boolean
  computerReviewAvailable?: boolean
  computerReviewOpen?: boolean
  mode?: 'full' | 'studio'
  onComputerReviewToggle?: () => void
}) {
  const operator = useOperator()
  const state = useOperatorState()
  const queryClient = useQueryClient()
  const location = useLocation()
  const navigate = useNavigate()
  const composerRef = useRef<HTMLTextAreaElement>(null)
  const transcriptRef = useRef<HTMLDivElement>(null)
  const seenEvents = useRef(new Set<string>())
  const [draft, setDraft] = useState('')
  const [messages, setMessages] = useState<DisplayMessage[]>([])
  const [reasoning, setReasoning] = useState<Record<string, string>>({})
  const [queue, setQueue] = useState<QueuedSubmission[]>([])
  const [busy, setBusy] = useState(false)
  const [detailsOpen, setDetailsOpen] = useState(false)
  const [notice, setNotice] = useState<string>()

  useEffect(() => {
    const prefill = (event: Event) => {
      const prompt = (event as CustomEvent<unknown>).detail
      if (typeof prompt !== 'string' || prompt.trim() === '') return
      setDraft(prompt)
      requestAnimationFrame(() => composerRef.current?.focus())
    }
    window.addEventListener('ion:prefill-chat', prefill)
    return () => window.removeEventListener('ion:prefill-chat', prefill)
  }, [])

  const scopedEvents = state.recent_events.filter(
    (event) =>
      operator.sessionID !== undefined &&
      event.correlation.session_id === operator.sessionID,
  )
  const runningTurns = Object.values(state.turns).filter(
    (turn) =>
	  (turn.status === 'running' || turn.status === 'recovering') &&
      operator.sessionID !== undefined &&
      turn.session_id === operator.sessionID,
  )
  const retryableTurns = Object.values(state.turns)
    .filter(
      (turn) =>
		(turn.status === 'failed' || turn.status === 'incomplete' || turn.status === 'interrupted') &&
        operator.sessionID !== undefined &&
        turn.session_id === operator.sessionID,
    )
    .sort((left, right) => left.last_sequence - right.last_sequence)
  const visibleTurnIDs = new Set(
    messages.flatMap((message) => message.turnID === undefined ? [] : [message.turnID]),
  )
  const toolEvents = scopedEvents.filter((event) => event.type.startsWith('tool.'))
  const epistemicEvents = scopedEvents.filter(
    (event) =>
      event.type.startsWith('premise.') ||
      event.type.startsWith('prediction.') ||
      event.type === 'convergence.warning',
  )
  const taskEvents = scopedEvents.filter(
    (event) => event.type.startsWith('task.') || event.type.startsWith('agent.'),
  )
	const terminalEvents = scopedEvents.filter(isTerminalTurnEvent)
  const activityCount = toolEvents.length + epistemicEvents.length + taskEvents.length

  useEffect(() => {
    seenEvents.current = new Set()
    setMessages([])
    setReasoning({})
    setNotice(undefined)
    const sessionID = operator.sessionID
    if (sessionID === undefined) return
    let disposed = false
    void operator
      .command<ResumedSession>(
        'session.resume',
        {},
        crypto.randomUUID(),
        { session_id: sessionID },
      )
      .then((response) => {
        if (disposed || response.error !== undefined) return
        const durable = (response.result?.messages ?? [])
          .filter(
            (message) =>
              (message.memory_type === undefined ||
                message.memory_type === 'transcript') &&
              (message.role === 'user' || message.role === 'assistant'),
          )
          .map<DisplayMessage>((message) => ({
            id: message.id,
            role: message.role === 'user' ? 'user' : 'assistant',
            content: message.content,
            createdAt: message.created_at,
            ...(message.turn_id === undefined ? {} : { turnID: message.turn_id }),
          }))
        const durableReasoning: Record<string, string> = {}
        for (const message of response.result?.messages ?? []) {
          if (
            message.memory_type !== 'summary' ||
            message.turn_id === undefined ||
            message.content.trim() === ''
          ) {
            continue
          }
          durableReasoning[message.turn_id] = message.content
        }
        setMessages((current) => mergeMessages(durable, current))
        setReasoning((current) => ({ ...durableReasoning, ...current }))
      })
    return () => {
      disposed = true
    }
  }, [operator.sessionID])

  useEffect(() => {
    for (const event of scopedEvents) {
      if (
        (event.type !== 'turn.delta' && event.type !== 'reasoning.summary') ||
        seenEvents.current.has(event.event_id)
      ) continue
      seenEvents.current.add(event.event_id)
      const turnID = event.correlation.turn_id ?? event.event_id
      const payload = record(event.payload)
      const reset = payload?.reset === true
      const replace = payload?.replace === true
      const content = text(record(event.payload)?.content)
      if (event.type === 'reasoning.summary') {
        if (payload?.source !== 'safe_summary') continue
        setReasoning((current) => {
          const next = { ...current }
          if (reset) delete next[turnID]
          else if (replace) {
            if (content === undefined || content === '') delete next[turnID]
            else next[turnID] = content
          } else if (content !== undefined) {
            next[turnID] = appendStreamText(next[turnID] ?? '', content)
          }
          return next
        })
        continue
      }
      setMessages((current) => {
        const id = `turn:${turnID}`
        if (reset) return current.filter((message) => message.id !== id)
        if (content === undefined || content === '') return current
        const existing = current.findIndex((message) => message.id === id)
        if (replace) {
          if (existing < 0) {
            return [...current, {
              id, role: 'assistant', content,
              createdAt: event.occurred_at, turnID,
            }]
          }
          const next = [...current]
          const message = next[existing]
          if (message !== undefined) {
            next[existing] = { ...message, content }
          }
          return next
        }
        if (existing < 0) {
          return [...current, {
            id, role: 'assistant', content,
            createdAt: event.occurred_at, turnID,
          }]
        }
        const next = [...current]
        const message = next[existing]
        if (message !== undefined) {
          next[existing] = { ...message, content: message.content + content }
        }
        return next
      })
    }
  }, [state.last_sequence, operator.sessionID])

  useEffect(() => {
    const node = transcriptRef.current
    if (node === null) return
    if (typeof node.scrollTo === 'function') {
      node.scrollTo({ top: node.scrollHeight, behavior: 'smooth' })
    } else {
      node.scrollTop = node.scrollHeight
    }
  }, [messages, reasoning, runningTurns.length, queue.length])

  const branchFrom = async (
    message: DisplayMessage,
    replacement?: string,
    previousMessageID?: string,
  ) => {
    if (operator.sessionID === undefined || busy) return
    setBusy(true)
    setNotice(undefined)
    try {
      const payload: Record<string, unknown> =
        replacement === undefined
          ? isUUID(message.id) ? { through_message_id: message.id } : {}
          : previousMessageID === undefined
            ? { copy_messages: false }
            : { through_message_id: previousMessageID }
      const branched = await operator.command<SessionResult>(
        'session.branch',
        payload,
        crypto.randomUUID(),
        { session_id: operator.sessionID },
      )
      if (branched.error !== undefined || branched.result?.id === undefined) {
        throw new Error(branched.error?.message ?? 'I could not fork this conversation.')
      }
      const sessionID = branched.result.id
      operator.setSessionID(sessionID)
      if (replacement !== undefined) {
        const projectID = mode === 'studio'
          ? studioProjectID(location.pathname)
          : undefined
        if (mode === 'studio' && projectID === undefined) {
          throw new Error('Open a registered Studio project before editing this turn.')
        }
        const submitted = await operator.command<TurnResult>(
          'turn.submit',
          {
            content: replacement,
            surface: mode === 'studio' ? 'studio' : 'general',
            ...(projectID === undefined ? {} : { project_id: projectID }),
          },
          crypto.randomUUID(),
          { session_id: sessionID },
        )
        if (submitted.error !== undefined) throw new Error(submitted.error.message)
      }
      setNotice(
        replacement === undefined
          ? 'Forked into a new conversation.'
          : 'Your edit is running in a new conversation.',
      )
    } catch (error) {
      setNotice(error instanceof Error ? error.message : String(error))
    } finally {
      setBusy(false)
    }
  }

  const send = async (
    content: string,
    submission: QueuedSubmission = { content, mode, pathname: location.pathname },
  ) => {
    setBusy(true)
    setNotice(undefined)
    try {
      let sessionID = operator.sessionID
      if (sessionID === undefined) {
        const created = await operator.command<SessionResult>(
          'session.create',
          {},
          crypto.randomUUID(),
        )
        if (created.error !== undefined || created.result?.id === undefined) {
          throw new Error(created.error?.message ?? 'I could not start a new conversation.')
        }
        sessionID = created.result.id
        operator.setSessionID(sessionID)
      }
      let projectID: string | undefined
      if (submission.mode === 'studio') {
        projectID = studioProjectID(submission.pathname)
        if (projectID === undefined) {
          if (submission.pathname !== '/studio') {
            throw new Error('This Studio location is not bound to a project.')
          }
          const projectName = conciseProjectName(content)
          const createdProject = await operator.command<StudioProjectResult>(
            'project.create',
            {
              name: projectName === '' ? 'Untitled project' : projectName,
              template: 'empty',
              host: 'direct_local',
              trust: 'reviewed',
            },
            crypto.randomUUID(),
          )
          if (
            createdProject.error !== undefined ||
            createdProject.result?.id === undefined
          ) {
            throw new Error(
              createdProject.error?.message ?? 'The Studio project could not be created.',
            )
          }
          projectID = createdProject.result.id
          await queryClient.invalidateQueries({ queryKey: ['studio', 'projects'] })
          navigate(`/studio/${projectID}`)
        }
      }
      const submitted = await operator.command<TurnResult>(
        'turn.submit',
        {
          content,
          surface: submission.mode === 'studio' ? 'studio' : 'general',
          ...(projectID === undefined ? {} : { project_id: projectID }),
        },
        crypto.randomUUID(),
        { session_id: sessionID },
      )
      if (submitted.error !== undefined) throw new Error(submitted.error.message)
      await queryClient.invalidateQueries({ queryKey: ['session.list'] })
      setMessages((current) => [
        ...current,
        {
          id: `local:${submitted.result?.turn_id ?? crypto.randomUUID()}`,
          role: 'user',
          content,
          createdAt: new Date().toISOString(),
        },
      ])
      setDraft('')
    } catch (error) {
      setNotice(error instanceof Error ? error.message : String(error))
    } finally {
      setBusy(false)
    }
  }

  useEffect(() => {
    if (runningTurns.length !== 0 || queue.length === 0 || busy) return
    const [next, ...remaining] = queue
    if (next === undefined) return
    setQueue(remaining)
    void send(next.content, next)
  }, [busy, queue, runningTurns.length])

  const submit = (event?: FormEvent) => {
    event?.preventDefault()
    const content = draft.trim()
    if (content === '') return
    if (runningTurns.length > 0 || busy) {
      setQueue((items) => [...items, { content, mode, pathname: location.pathname }])
      setDraft('')
      setNotice('Added to the queue. Ion will start it next.')
      return
    }
    void send(content)
  }

  const onComposerKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      submit()
    }
  }

  const updateDraft = (value: string) => {
    setDraft(value)
    const composer = composerRef.current
    if (composer === null) return
    composer.style.height = 'auto'
    composer.style.height = `${String(Math.min(composer.scrollHeight, 220))}px`
  }

  const chooseStarter = (prompt: string) => {
    updateDraft(prompt)
    requestAnimationFrame(() => composerRef.current?.focus())
  }

  const cancelLatest = async () => {
    const latest = runningTurns.at(-1)
    if (latest === undefined || operator.sessionID === undefined) return
    const response = await operator.command(
      'turn.cancel',
      { turn_id: latest.id },
      crypto.randomUUID(),
      { session_id: operator.sessionID },
    )
    setNotice(response.error?.message ?? 'Stopping the current response…')
  }

  const steerLatest = async () => {
    const latest = runningTurns.at(-1)
    const content = draft.trim()
    if (
      latest === undefined ||
      operator.sessionID === undefined ||
      content === '' ||
      busy
    ) {
      return
    }
    const target = currentSteerTarget(latest.id, scopedEvents)
    if (target === undefined) {
      setNotice('Live execution evidence is still catching up. Try steering again in a moment.')
      return
    }
    setBusy(true)
    setNotice(undefined)
    try {
      const response = await operator.command<TurnResult>(
        'turn.steer',
        { turn_id: latest.id, content, target },
        crypto.randomUUID(),
        { session_id: operator.sessionID },
      )
      if (response.error !== undefined) throw new Error(response.error.message)
      setDraft('')
      setNotice('Ion is adjusting to your new direction.')
    } catch (error) {
      setNotice(error instanceof Error ? error.message : String(error))
    } finally {
      setBusy(false)
    }
  }

  const retryLatest = async () => {
    const latest = retryableTurns.at(-1)
    if (
      latest === undefined ||
      operator.sessionID === undefined ||
      runningTurns.length > 0 ||
      busy
    ) {
      return
    }
    setBusy(true)
    setNotice(undefined)
    try {
      const response = await operator.command<TurnResult>(
        'turn.retry',
        { turn_id: latest.id },
        crypto.randomUUID(),
        { session_id: operator.sessionID },
      )
      if (response.error !== undefined) throw new Error(response.error.message)
      setNotice('Trying that request again…')
    } catch (error) {
      setNotice(error instanceof Error ? error.message : String(error))
    } finally {
      setBusy(false)
    }
  }

  const isEmpty = messages.length === 0 && runningTurns.length === 0
  const composer = (
    <div className="composer-region">
      {queue.length > 0 ? (
        <div className="queue" aria-label="Queued messages">
          <strong>{queue.length} waiting</strong>
          {queue.map((item, index) => (
            <span key={`${item.content}-${String(index)}`}>{item.content}</span>
          ))}
        </div>
      ) : null}
      {notice === undefined ? null : (
        <p className="composer-notice" role="status">{notice}</p>
      )}
      <form className="composer" onSubmit={submit}>
        <label className="sr-only" htmlFor="operator-message">Message Ion</label>
        <textarea
          id="operator-message"
          onChange={(event) => updateDraft(event.target.value)}
          onKeyDown={onComposerKeyDown}
          placeholder={
            mode === 'studio'
              ? studioProjectID(location.pathname) === undefined
                ? 'Describe what to build; an isolated project will be created…'
                : 'Continue work on this project…'
              : 'Ask anything, or give Ion a goal…'
          }
          ref={composerRef}
          rows={1}
          value={draft}
        />
        <div className="composer-actions">
          <details className="composer-more">
            <summary aria-label="Add context">
              <Icon name="plus" />
            </summary>
            <div className="composer-menu">
              <strong>Add context</strong>
              <Link to="/knowledge"><Icon name="brain" /> Saved knowledge</Link>
              <Link to="/work"><Icon name="folder" /> Project or task</Link>
              <Link to="/extensions"><Icon name="workflow" /> Connected service</Link>
            </div>
          </details>
          <span className="composer-mode"><Icon name="spark" /> {
            mode === 'studio'
              ? studioProjectID(location.pathname) === undefined
                ? 'New Studio project'
                : 'Studio project'
              : 'Full agent'
          }</span>
          <span className="composer-hint">Enter to send · Shift+Enter for a new line</span>
          {runningTurns.length > 0 ? (
            <>
              <button
                aria-label="Cancel turn"
                className="composer-icon-button"
                disabled={busy}
                onClick={() => void cancelLatest()}
                title="Stop response"
                type="button"
              >
                <Icon name="stop" />
              </button>
              {draft.trim() === '' ? null : (
                <button
                  aria-label="Steer active turn"
                  className="steer-button"
                  disabled={busy}
                  onClick={() => void steerLatest()}
                  type="button"
                >
                  Update direction
                </button>
              )}
            </>
		  ) : retryableTurns.length > 0 && terminalEvents.length > 0 ? (
            <button
              aria-label="Retry last turn"
              className="retry-button"
              disabled={busy}
              onClick={() => void retryLatest()}
              type="button"
            >
              Try again
            </button>
          ) : null}
          <button
            aria-label={runningTurns.length > 0 ? 'Queue' : 'Send'}
            className="send-button"
            disabled={busy || draft.trim() === ''}
            type="submit"
          >
            <Icon name="arrow-up" />
          </button>
        </div>
      </form>
      <p className="composer-disclaimer">
        Ion can take action. Review important decisions and sources.
      </p>
    </div>
  )

  return (
    <section
      aria-label="Persistent operator chat"
      className="chat-host"
      data-active={active}
      data-details-open={detailsOpen}
      data-empty={isEmpty}
      data-mode={mode}
      data-testid="persistent-chat-host"
    >
      <div className="conversation-shell">
        <div className="conversation-toolbar">
          {mode === 'full' &&
          computerReviewAvailable &&
          onComputerReviewToggle !== undefined ? (
            <button
              aria-expanded={computerReviewOpen}
              className="quiet-button activity-toggle"
              onClick={onComputerReviewToggle}
              type="button"
            >
              <Icon name={computerReviewOpen ? 'panel-left-open' : 'history'} />
              <span>{computerReviewOpen ? 'Hide Computer' : 'Review Computer'}</span>
            </button>
          ) : null}
          <button
            aria-expanded={detailsOpen}
            className="quiet-button activity-toggle"
            onClick={() => setDetailsOpen((value) => !value)}
            type="button"
          >
            <Icon name="activity" />
            <span>Activity</span>
            {activityCount > 0 ? <b>{activityCount}</b> : null}
          </button>
        </div>

        <div className="transcript" ref={transcriptRef} aria-live="polite" aria-relevant="additions">
          <div className="transcript-inner">
            <h1 className="sr-only">Chat</h1>
            {isEmpty ? (
              <div className="empty-conversation">
                <EmptyConversation />
                {composer}
                <StarterSuggestions onChoose={chooseStarter} />
              </div>
            ) : (
              <>
                {messages.map((message, index) => (
                  <Message
                    key={message.id}
                    message={message}
                    {...(message.turnID === undefined ||
                    reasoning[message.turnID] === undefined
                      ? {}
                      : { reasoning: reasoning[message.turnID] })}
                    onEdit={(replacement) => void branchFrom(
                      message,
                      replacement,
                      messages.slice(0, index).reverse().find((candidate) => isUUID(candidate.id))?.id,
                    )}
                    onFork={() => void branchFrom(message)}
                  />
                ))}
				{terminalEvents.slice(-1).map((event) => (
                  <FailureNotice event={event} key={event.event_id} />
                ))}
                {runningTurns.map((turn) =>
                  visibleTurnIDs.has(turn.id) || reasoning[turn.id] === undefined
                    ? null
                    : <LiveTurnProgress key={turn.id} reasoning={reasoning[turn.id] ?? ''} />,
                )}
              </>
            )}
            <ApprovalStack
              {...(operator.sessionID === undefined
                ? {}
                : { sessionID: operator.sessionID })}
            />
            {runningTurns.length > 0 ? (
              <div className="working-indicator" role="status">
                <span className="assistant-avatar small" aria-hidden="true"><Icon name="spark" /></span>
                <span className="thinking-dots"><i /><i /><i /></span>
				<span>{runningTurns.some((turn) => turn.status === 'recovering')
				  ? 'Ion is recovering and continuing from saved work'
				  : 'Ion is working'}</span>
              </div>
            ) : null}
          </div>
        </div>

        {isEmpty ? null : composer}
      </div>

      <aside className="activity-drawer" aria-label="Work details">
        <div className="drawer-heading">
          <div>
            <strong>Work details</strong>
            <span>What Ion used and did</span>
          </div>
          <button
            aria-label="Close work details"
            className="icon-button"
            onClick={() => setDetailsOpen(false)}
            type="button"
          >
            <Icon name="close" />
          </button>
        </div>
        <ActivitySection title="Actions" events={toolEvents} empty="No actions used yet." />
        <ActivitySection
          title="Plan & helpers"
          events={taskEvents}
          empty="No delegated work yet."
        />
        <ActivitySection
          title="Assumptions & forecasts"
          events={epistemicEvents}
          empty="No assumptions recorded yet."
        />
        <ActivationInspector snapshot={state.snapshot} />
      </aside>
    </section>
  )
}

function appendStreamText(current: string, incoming: string): string {
  if (incoming === '' || current.endsWith(incoming)) return current
  return current + incoming
}

function LiveTurnProgress({ reasoning }: { reasoning: string }) {
  if (reasoning.trim() === '') return null
  return (
    <article className="message assistant live-turn-progress">
      <span className="assistant-avatar small" aria-hidden="true"><Icon name="spark" /></span>
      <div className="message-body">
        <details className="reasoning-block" open>
          <summary><Icon name="brain" /> Reasoning summary</summary>
          <div>{reasoning}</div>
        </details>
      </div>
    </article>
  )
}

function EmptyConversation() {
  return (
    <div className="chat-welcome">
      <span className="welcome-kicker">Ion</span>
      <h2>What can I do for you?</h2>
      <p>
        Ask a question, start a project, or hand me a complex goal.
      </p>
    </div>
  )
}

function StarterSuggestions({ onChoose }: { onChoose(prompt: string): void }) {
  return (
    <div className="starter-grid" aria-label="Suggested starting points">
      <span className="starter-label">Suggested</span>
      <div>
        {starters.map((starter) => (
          <button key={starter.title} onClick={() => onChoose(starter.prompt)} type="button">
            <Icon name={starter.title === 'Plan a project' ? 'folder' : starter.title === 'Research something' ? 'search' : starter.title === 'Create something' ? 'spark' : 'brain'} />
            <span>
              <strong>{starter.title}</strong>
              <small>{starter.prompt.replace(': ', '').split(',')[0]}</small>
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}

export function Message({
  message,
  reasoning,
  onEdit,
  onFork,
}: {
  message: DisplayMessage
  reasoning?: string
  onEdit(replacement: string): void
  onFork(): void
}) {
  const [copied, setCopied] = useState(false)
  const [editing, setEditing] = useState(false)
  const [editDraft, setEditDraft] = useState(message.content)
  const [speaking, setSpeaking] = useState(false)
  const copy = async () => {
    await navigator.clipboard.writeText(message.content)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1_500)
  }
  const share = async () => {
    if (typeof navigator.share === 'function') {
      await navigator.share({ title: 'Ion response', text: message.content })
      return
    }
    await navigator.clipboard.writeText(message.content)
    setCopied(true)
  }
  const readAloud = () => {
    if (!('speechSynthesis' in window) || typeof SpeechSynthesisUtterance === 'undefined') return
    if (speaking) {
      window.speechSynthesis.cancel()
      setSpeaking(false)
      return
    }
    const utterance = new SpeechSynthesisUtterance(message.content)
    utterance.onend = () => setSpeaking(false)
    utterance.onerror = () => setSpeaking(false)
    window.speechSynthesis.cancel()
    window.speechSynthesis.speak(utterance)
    setSpeaking(true)
  }
  if (message.role === 'user') {
    return (
      <article className="message user">
        <div className="user-message-wrap">
          {editing ? (
            <form
              className="message-edit"
              onSubmit={(event) => {
                event.preventDefault()
                const replacement = editDraft.trim()
                if (replacement === '') return
                setEditing(false)
                onEdit(replacement)
              }}
            >
              <textarea onChange={(event) => setEditDraft(event.target.value)} value={editDraft} />
              <div>
                <button onClick={() => setEditing(false)} type="button">Cancel</button>
                <button type="submit">Send edit</button>
              </div>
            </form>
          ) : <div className="message-content">{message.content}</div>}
          <MessageActions
            copied={copied}
            onCopy={() => void copy()}
            onEdit={() => setEditing(true)}
            onFork={onFork}
            onRead={readAloud}
            onShare={() => void share()}
            speaking={speaking}
          />
        </div>
      </article>
    )
  }
  return (
    <article className="message assistant">
      <span className="assistant-avatar small" aria-hidden="true"><Icon name="spark" /></span>
      <div className="message-body">
        {reasoning === undefined || reasoning.trim() === '' ? null : (
          <details className="reasoning-block">
            <summary><Icon name="brain" /> Reasoning summary</summary>
            <div>{reasoning}</div>
          </details>
        )}
        <MarkdownResponse content={message.content} />
        <MessageActions
          copied={copied}
          onCopy={() => void copy()}
          onFork={onFork}
          onRead={readAloud}
          onShare={() => void share()}
          speaking={speaking}
        />
      </div>
    </article>
  )
}

function MessageActions({
  copied,
  onCopy,
  onEdit,
  onFork,
  onRead,
  onShare,
  speaking,
}: {
  copied: boolean
  onCopy(): void
  onEdit?(): void
  onFork(): void
  onRead(): void
  onShare(): void
  speaking: boolean
}) {
  return (
    <div className="message-actions">
      <button onClick={onCopy} type="button"><Icon name={copied ? 'check' : 'archive'} /><span>{copied ? 'Copied' : 'Copy'}</span></button>
      {onEdit === undefined ? null : <button onClick={onEdit} type="button"><Icon name="edit" /><span>Edit</span></button>}
      <button onClick={onFork} type="button"><Icon name="fork" /><span>Fork</span></button>
      <button onClick={onShare} type="button"><Icon name="share" /><span>Share</span></button>
      <button onClick={onRead} type="button"><Icon name="volume" /><span>{speaking ? 'Stop' : 'Read aloud'}</span></button>
    </div>
  )
}

function FailureNotice({ event }: { event: EventEnvelope }) {
  const payload = record(event.payload) ?? {}
  const message = failureMessage(payload)
  return (
    <article className="conversation-callout danger">
      <strong>{message.title}</strong>
      <span>{message.detail}</span>
      <details>
        <summary>Technical details</summary>
        <code>{event.type}: {compactPayload(payload)}</code>
      </details>
    </article>
  )
}

export function isTerminalTurnEvent(event: EventEnvelope): boolean {
  if (event.type === 'turn.failed') return true
  if (event.type !== 'turn.incomplete') return false
  return record(event.payload)?.final_honest_partial !== false
}

export function failureMessage(payload: Record<string, unknown>): {
  title: string
  detail: string
} {
  const code = text(payload.error_code)
  const failureClass = text(payload.error_class)
	const phase = text(payload.phase)
	if (phase === 'provider_tool_markup') {
	  return {
		title: 'Work stopped; it is not still running',
		detail: 'The provider returned an action in an incompatible format. Completed actions are saved, and Try again resumes from that durable point.',
	  }
	}
	if (phase === 'evidence_convergence') {
	  return {
		title: 'Work paused before verified completion',
		detail: 'Required criteria still lack server-verified evidence. Completed work is saved and the next attempt resumes the unfinished criterion.',
	  }
	}
  if (code === 'provider_payment_required') {
    return {
      title: 'The model provider needs attention',
      detail:
        'The provider refused this request because the account needs payment or credits. Update the provider account, then try again.',
    }
  }
  if (failureClass === 'rate_limit') {
    return {
      title: 'The model is temporarily busy',
      detail: 'No action was lost. Wait a moment, then try again.',
    }
  }
  if (failureClass === 'timeout' || failureClass === 'transient') {
    return {
      title: 'The connection ended before the answer was ready',
      detail: 'No action was lost. Check the connection, then try again.',
    }
  }
  return {
    title: 'The request could not be completed',
    detail:
      'The turn did not finish. Review Work details because some actions may already have completed, then try again.',
  }
}

export function MarkdownResponse({ content }: { content: string }) {
  return (
    <div className="rich-text">
      <Markdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          a: ({ children, href, node: _node, ...props }) => {
            const external =
              href?.startsWith('https://') === true ||
              href?.startsWith('http://') === true
            return (
              <a
                {...props}
                href={href}
                {...(external
                  ? { rel: 'noopener noreferrer', target: '_blank' }
                  : {})}
              >
                {children}
              </a>
            )
          },
        }}
      >
        {content}
      </Markdown>
    </div>
  )
}

function ApprovalStack({ sessionID }: { sessionID?: string }) {
  const { command } = useOperator()
  const state = useOperatorState()
  const approvals = Object.values(state.pending_approvals).filter(
    (approval) => sessionID === undefined || approval.session_id === sessionID,
  )
  const [responding, setResponding] = useState<string>()
  if (approvals.length === 0) return null
  const respond = async (approvalID: string, decision: 'approve' | 'deny') => {
    setResponding(approvalID)
    const approval = approvals.find((item) => item.id === approvalID)
    const scope =
      approval?.session_id === undefined ? {} : { session_id: approval.session_id }
    await command(
      'approval.respond',
      { approval_id: approvalID, decision },
      crypto.randomUUID(),
      scope,
    )
    setResponding(undefined)
  }
  return (
    <div className="approval-stack">
      {approvals.map((approval) => (
        <article className="approval-card" key={approval.id}>
          <div className="approval-icon" aria-hidden="true"><Icon name="shield" /></div>
          <div className="approval-content">
            <span className="risk-label">YOUR DECISION IS REQUIRED</span>
            <h3>
              {text(approval.payload.title) ??
                text(approval.payload.operation) ??
                'Ion wants to take an important action'}
            </h3>
            <p>
              {text(approval.payload.consequence) ??
                'Review what will happen before Ion continues.'}
            </p>
            <details className="approval-details">
              <summary>Review technical details</summary>
              <dl>
                <div>
                  <dt>Technical operation</dt>
                  <dd>{text(approval.payload.operation) ?? 'Not provided'}</dd>
                </div>
                <div>
                  <dt>Actor</dt>
                  <dd>{text(approval.payload.actor_id) ?? 'Ion'}</dd>
                </div>
                <div>
                  <dt>Expires</dt>
                  <dd>{formatTimestamp(text(approval.payload.expires_at))}</dd>
                </div>
              </dl>
              <pre aria-label="Redacted operation arguments">
                <code>{JSON.stringify(approval.payload.arguments ?? {}, null, 2)}</code>
              </pre>
            </details>
            <div className="approval-actions">
              <button
                aria-label="Deny"
                className="deny-button"
                disabled={responding === approval.id}
                onClick={() => void respond(approval.id, 'deny')}
                type="button"
              >
                Don&apos;t allow
              </button>
              <button
                disabled={responding === approval.id}
                onClick={() => void respond(approval.id, 'approve')}
                type="button"
              >
                Approve this action
              </button>
            </div>
          </div>
        </article>
      ))}
    </div>
  )
}

function ActivitySection({
  title,
  events,
  empty,
}: {
  title: string
  events: EventEnvelope[]
  empty: string
}) {
  return (
    <section className="drawer-section">
      <div className="drawer-section-heading">
        <h2>{title}</h2>
        <span>{events.length}</span>
      </div>
      {events.length === 0 ? (
        <p className="empty-copy">{empty}</p>
      ) : (
        <div className="event-stack">
          {events.slice(-20).reverse().map((event) => (
            <article className="activity-event" key={event.event_id}>
              <span className="event-icon" aria-hidden="true"><Icon name="check" /></span>
              <div>
                <strong>{eventTitle(event.type)}</strong>
                <span>{formatTimestamp(event.occurred_at)}</span>
                <details>
                  <summary>Details</summary>
                  <code>{event.type}: {activityDetails(event)}</code>
                </details>
              </div>
            </article>
          ))}
        </div>
      )}
    </section>
  )
}

function ActivationInspector({ snapshot }: { snapshot?: unknown }) {
  const tiers = record(record(snapshot)?.activation)?.tiers
  return (
    <section className="drawer-section">
      <div className="drawer-section-heading">
        <h2>Knowledge used</h2>
      </div>
      <p className="empty-copy">
        {tiers === undefined
          ? 'Relevant saved knowledge will appear here when it helps with an answer.'
          : 'Ion used saved context for this work.'}
      </p>
      {tiers === undefined ? null : (
        <details>
          <summary>View knowledge details</summary>
          <code>{JSON.stringify(tiers)}</code>
        </details>
      )}
    </section>
  )
}

function mergeMessages(
  first: DisplayMessage[],
  second: DisplayMessage[],
): DisplayMessage[] {
  const merged: DisplayMessage[] = []
  const seenIDs = new Set<string>()
  const seenContent = new Set<string>()
  for (const message of [...first, ...second]) {
    const contentKey = `${message.role}:${message.content}`
    if (seenIDs.has(message.id) || seenContent.has(contentKey)) continue
    seenIDs.add(message.id)
    seenContent.add(contentKey)
    merged.push(message)
  }
  return merged
}

function currentSteerTarget(turnID: string, events: EventEnvelope[]) {
  const turnEvents = events.filter(
    (event) => event.correlation.turn_id === turnID,
  )
  const latest = turnEvents.at(-1)
  if (latest === undefined) return undefined
  const payload = isComputerEventPayload(latest.payload)
    ? latest.payload
    : undefined
  if (
    payload !== undefined &&
    !['completed', 'failed', 'denied', 'interrupted', 'outcome_unknown']
      .includes(payload.phase)
  ) {
    const taskID =
      payload.scope.task_id ??
      payload.scope.outcome_id ??
      payload.scope.turn_id
    if (taskID === undefined) return undefined
    return {
      kind: 'tool',
      task_id: taskID,
      agent_id: payload.scope.agent_id,
      tool_event_id: payload.tool_event_id,
      tool_action: payload.operation,
      target_revision: latest.sequence,
    }
  }
  return {
    kind: 'turn',
    task_id: turnID,
    agent_id: 'ion',
    tool_action: 'turn.run',
    target_revision: latest.sequence,
  }
}

function record(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

function compactPayload(value: Record<string, unknown>): string {
  const encoded = JSON.stringify(value)
  return encoded.length > 180 ? `${encoded.slice(0, 177)}…` : encoded
}

function activityDetails(event: EventEnvelope): string {
  if (isComputerEventPayload(event.payload)) {
    const compatibility = event.payload.display_model === undefined
      ? undefined
      : displayModelCompatibility(
          event.payload.display_model,
          event.payload.source_references.length,
        )
    const displayModel = compatibility === 'current' || compatibility === 'migrated'
      ? migrateDisplayModel(
          event.payload.display_model,
          event.payload.source_references.length,
        )
      : undefined
    return JSON.stringify({
      tool: event.payload.tool,
      phase: event.payload.phase,
      terminal_status: event.payload.terminal_status,
      result: event.payload.result,
      display: compatibility === 'unsupported'
        ? { status: 'unsupported_version' }
        : displayModel === undefined
          ? undefined
          : {
              compatibility,
              kind: displayModel.kind,
              title: displayModel.title,
              fields: displayModel.fields,
              blocks: displayModel.blocks,
            },
      source_references: event.payload.source_references,
    })
  }
  if (event.type.startsWith('tool.')) {
    return JSON.stringify({
      display: { status: 'unsupported_lifecycle_version' },
    })
  }
  return compactPayload(record(event.payload) ?? {})
}

function formatTimestamp(value: string | undefined): string {
  if (value === undefined) return 'Unknown'
  const date = new Date(value)
  return Number.isNaN(date.valueOf())
    ? value
    : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function isUUID(value: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value)
}

function studioProjectID(pathname: string): string | undefined {
  const match = /^\/studio\/([0-9a-f-]+)$/i.exec(pathname)
  const projectID = match?.[1]
  return projectID !== undefined && isUUID(projectID) ? projectID : undefined
}

function eventTitle(type: string): string {
  const labels: Record<string, string> = {
    'agent.started': 'A helper started work',
    'agent.completed': 'A helper finished',
    'convergence.warning': 'Ion reconsidered its approach',
    'memory.activated': 'Saved knowledge was used',
    'prediction.created': 'A forecast was recorded',
    'premise.created': 'An assumption was recorded',
    'task.completed': 'A task finished',
    'task.started': 'A task started',
    'tool.completed': 'An action finished',
    'tool.failed': 'An action needs attention',
    'tool.denied': 'An action was denied',
    'tool.interrupted': 'An action was interrupted',
    'tool.outcome_unknown': 'An action has an uncertain outcome',
    'tool.awaiting_approval': 'An action is awaiting your decision',
    'tool.requested': 'An action was prepared',
    'tool.started': 'An action started',
  }
  return labels[type] ?? type.split('.').map(humanize).join(' · ')
}

function humanize(value: string): string {
  const spaced = value.replaceAll('_', ' ')
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}
