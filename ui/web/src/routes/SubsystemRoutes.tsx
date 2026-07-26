import { useQueries, useQueryClient } from '@tanstack/react-query'
import type { Operation } from '@matrixmcl/ion-shared'
import { useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { useOperator } from '../app/operator-context'
import { Icon } from '../components/Icon'

export interface SurfaceDefinition {
  path: string
  eyebrow: string
  title: string
  description: string
  operations: readonly Operation[]
  accent: 'sage' | 'amber' | 'violet' | 'cyan'
}

export const subsystemSurfaces: readonly SurfaceDefinition[] = [
  {
    path: '/since-away',
    eyebrow: 'CONTINUITY',
    title: 'Since you were away',
    description:
      'Review completed work, failures, decisions, changed files, pending questions, and deadlines from durable evidence.',
    operations: ['continuity.brief'],
    accent: 'sage',
  },
  {
    path: '/knowledge',
    eyebrow: 'KNOWLEDGE',
    title: 'What Ion knows',
    description:
      'Review saved knowledge, its sources and confidence, and what Ion expects to happen next.',
    operations: [
      'memory.search',
      'memory.graph',
      'memory.activation',
      'premise.list',
      'prediction.list',
      'curiosity.targets',
      'dreamweaver.beliefs',
    ],
    accent: 'violet',
  },
  {
    path: '/work',
    eyebrow: 'WORK',
    title: 'Goals and active work',
    description:
      'See what is planned, what is running, who is helping, and what needs your decision.',
    operations: [
	  'work.brief',
	  'artifact.list',
	  'autonomy.get',
	  'project.list',
	  'workflow.list',
	  'supervisor.list',
	  'workspace.capabilities',
	  'studio.intent.list',
      'taskgraph.get',
      'taskgraph.todo',
      'swarm.list',
      'automatrix.list',
    ],
    accent: 'sage',
  },
  {
    path: '/execution',
    eyebrow: 'ACTIONS',
    title: 'Actions and decisions',
    description:
      'Check which actions are available, why a decision was made, and what actually happened.',
    operations: [
      'tool.surface',
      'tool.readiness',
      'policy.events',
      'receipt.list',
      'receipt.verify',
    ],
    accent: 'amber',
  },
  {
    path: '/extensions',
    eyebrow: 'CONNECTIONS',
    title: 'Models and connections',
    description:
      'Manage the models, connected services, add-ons, and abilities Ion can use.',
    operations: [
      'provider.list',
      'provider.usage',
      'mcp.servers',
      'mcp.tools',
      'plugin.list',
      'skill.list',
      'skill.lifecycle',
      'tool.readiness',
    ],
    accent: 'cyan',
  },
  {
    path: '/presence',
    eyebrow: 'AVAILABILITY',
    title: 'Where and when Ion works',
    description:
      'Review connected channels, scheduled work, delivery health, and current availability.',
    operations: [
      'channel.list',
      'channel.health',
      'schedule.list',
      'liveness.get',
      'system.health',
      'system.metrics',
    ],
    accent: 'sage',
  },
  {
    path: '/identity',
    eyebrow: 'IDENTITY',
    title: 'Preferences and identity',
    description:
      'Review how Ion is configured and the values that keep its behavior consistent.',
    operations: ['config.get', 'soul.get', 'liveness.get', 'commands.catalog'],
    accent: 'violet',
  },
  {
    path: '/diagnostics',
    eyebrow: 'SYSTEM HEALTH',
    title: 'System health and activity',
    description:
      'Find recent activity, service health, performance, and safety-check results.',
    operations: [
      'logs.query',
      'system.health',
      'system.metrics',
      'integrity.latest',
      'commands.catalog',
    ],
    accent: 'amber',
  },
] as const

const wideSurfaceOperations = new Set<Operation>([
  'automatrix.list',
  'commands.catalog',
  'integrity.latest',
  'liveness.get',
  'continuity.brief',
  'logs.query',
  'policy.events',
  'studio.intent.list',
  'supervisor.list',
  'taskgraph.get',
  'tool.readiness',
  'tool.surface',
  'workflow.list',
  'workspace.capabilities',
])

const mutationForSurface: Partial<
  Record<
    string,
    {
      operation: Operation
      label: string
      payload: Record<string, unknown>
      consequence: string
    }
  >
> = {
  '/diagnostics': {
    operation: 'integrity.run',
    label: 'Run a safety check',
    payload: {},
    consequence: 'Ion will check recent records for missing or unexpected changes.',
  },
}

export function SubsystemPage({ surface }: { surface: SurfaceDefinition }) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [notice, setNotice] = useState<string>()
  const [continuityPeriod, setContinuityPeriod] = useState<'24h' | '7d' | '30d'>('24h')
  const queries = useQueries({
    queries: surface.operations.map((operation) => ({
      queryKey: ['surface', operation, operator.sessionID, continuityPeriod],
      queryFn: async () => {
        const response = await operator.client.query<unknown>(
          operation,
          defaultPayload(operation, continuityPeriod),
          operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
        )
        if (response.error !== undefined) throw new Error(response.error.message)
        return { operation, revision: response.revision, value: response.result ?? {} }
      },
      retry: false,
	  refetchInterval: operation === 'supervisor.list' ? 1000 : operation === 'work.brief' || operation === 'artifact.list' || operation === 'autonomy.get' ? 5000 : false,
    })),
  })
  const mutation = mutationForSurface[surface.path]
  const runMutation = async () => {
    if (mutation === undefined) return
    const response = await operator.command(
      mutation.operation,
      mutation.payload,
      crypto.randomUUID(),
      operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
    )
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    setNotice(`${mutation.label} completed.`)
    await queryClient.invalidateQueries({ queryKey: ['surface'] })
  }
  const runSupervisorCommand = async (
    operation: 'supervisor.steer' | 'supervisor.cancel',
    payload: Record<string, unknown>,
  ) => {
    const response = await operator.command(
      operation,
      payload,
      crypto.randomUUID(),
      operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
    )
    if (response.error !== undefined) {
      throw new Error(response.error.message)
    }
    setNotice(
      operation === 'supervisor.steer'
        ? 'Guidance added to the active supervised outcome.'
        : 'Supervised work cancelled.',
    )
    await queryClient.invalidateQueries({
      queryKey: ['surface', 'supervisor.list'],
    })
  }
  const states = queries.map((query) =>
    surfaceState(query.isPending, query.isError, query.data?.value),
  )
  const pending = states.filter((state) => state === 'pending').length
  const attention = states.filter((state) => state === 'error').length
  const checked = states.filter((state) => state === 'ready' || state === 'empty').length
  const allIndexes = surface.operations.map((_, index) => index)
  const primaryIndexes = surface.path === '/extensions'
    ? allIndexes.slice(0, 2)
    : allIndexes
  const advancedIndexes = surface.path === '/extensions'
    ? allIndexes.slice(2)
    : []
  const providerIndex = surface.operations.indexOf('provider.list')
  const pluginIndex = surface.operations.indexOf('plugin.list')
  const readinessIndex = surface.operations.indexOf('tool.readiness')
  const providerQuery = providerIndex < 0 ? undefined : queries[providerIndex]
  const pluginValue = pluginIndex < 0 ? undefined : queries[pluginIndex]?.data?.value
  const readinessValue = readinessIndex < 0 ? undefined : queries[readinessIndex]?.data?.value
  const providerSetupNeeded = surface.path === '/extensions' &&
    providerIndex >= 0 &&
    states[providerIndex] === 'empty'
  const renderCard = (index: number) => {
    const query = queries[index]
    const operation = surface.operations[index]
    if (query === undefined || operation === undefined) return null
    const state = states[index] ?? 'pending'
    const projection = projectionProblem(query.data?.value)
    const technicalError = query.error instanceof Error
      ? query.error.message
      : projection?.reason
    return (
      <article
        className={`data-surface state-${state}${wideSurfaceOperations.has(operation) ? ' surface-wide' : ''}`}
        data-operation={operation}
        key={operation}
      >
        <header>
          <div>
            <span className="operation-kicker">{operationCategory(operation)}</span>
            <h2>{operationTitle(operation)}</h2>
            <p>{operationDescription(operation)}</p>
          </div>
          <span className={`status-pill ${state === 'error' ? 'error' : state === 'pending' ? 'pending' : state === 'empty' ? 'quiet' : ''}`}>
            {statusLabel(state)}
          </span>
        </header>
        {state === 'error' ? (
          <div className="surface-empty">
            <strong>This information is unavailable</strong>
            <span>{friendlyProblem(operation, technicalError)}</span>
            <details className="technical-details">
              <summary>Technical details</summary>
              <code>{technicalError ?? 'No additional error information was provided.'}</code>
            </details>
          </div>
        ) : state === 'pending' ? (
          <div className="surface-skeleton" aria-label="Loading">
            <span /><span /><span />
          </div>
        ) : state === 'empty' ? (
          <div className="surface-empty">
            <strong>{emptyState(operation).title}</strong>
            <span>{emptyState(operation).description}</span>
            <TechnicalDetails
              operation={operation}
              revision={query.data?.revision}
              value={query.data?.value}
            />
          </div>
        ) : (
          <div className="surface-content">
            <StructuredValue
              onSupervisorCommand={
                operation === 'supervisor.list'
                  ? runSupervisorCommand
                  : undefined
              }
              operation={operation}
              value={query.data?.value}
            />
            <TechnicalDetails
              operation={operation}
              revision={query.data?.revision}
              value={query.data?.value}
            />
          </div>
        )}
      </article>
    )
  }
  return (
    <div className={`route-page subsystem-page accent-${surface.accent}`}>
      <header className="route-header subsystem-header">
        <div>
          <p className="eyebrow">{surface.eyebrow}</p>
          <h1>{surface.title}</h1>
          <p>{surface.description}</p>
        </div>
        <div className="surface-summary" aria-label="Information availability">
          <span className={`summary-status ${pending === 0 && attention === 0 && !providerSetupNeeded ? 'ready' : ''}`}>
            <span aria-hidden="true" />
            {pending > 0
              ? `Checking ${pending} ${pluralize('section', pending)}`
              : attention > 0
                ? `${attention} ${pluralize('section', attention)} ${attention === 1 ? 'needs' : 'need'} attention`
                : providerSetupNeeded
                  ? 'Setup needed'
                  : 'Up to date'}
          </span>
          <span className="sr-only">
            {checked} checked, {attention} unavailable, {pending} loading
          </span>
        </div>
      </header>
      {surface.path === '/since-away' ? (
        <div className="inline-actions" aria-label="Summary period">
          {(['24h', '7d', '30d'] as const).map((period) => (
            <button
              aria-pressed={continuityPeriod === period}
              className={continuityPeriod === period ? '' : 'secondary'}
              key={period}
              onClick={() => setContinuityPeriod(period)}
              type="button"
            >
              {period === '24h' ? 'Past day' : period === '7d' ? 'Past week' : 'Past month'}
            </button>
          ))}
        </div>
      ) : null}
      {mutation === undefined ? null : (
        <section className="action-ribbon" aria-label="Available action">
          <div>
            <strong>{mutation.label}</strong>
            <span>{mutation.consequence}</span>
          </div>
          <button onClick={() => void runMutation()} type="button">
            {mutation.label}
          </button>
        </section>
      )}
      {surface.path === '/extensions' ? (
        <>
          <ProviderConnectionGuide
            error={providerQuery?.error ?? null}
            pending={providerQuery?.isPending ?? true}
            value={providerQuery?.data?.value}
          />
          <BrowserMailboxGuide
            pending={readinessIndex < 0 || queries[readinessIndex]?.isPending === true}
            value={readinessValue}
          />
        </>
      ) : null}
      {surface.path === '/extensions' &&
      Array.isArray(pluginValue) &&
      pluginValue.length > 0 ? <PluginSandbox /> : null}
      {notice === undefined ? null : <p className="surface-notice" role="status">{notice}</p>}
      <section className="surface-grid" aria-label={`${surface.title} data`}>
        {primaryIndexes.map(renderCard)}
      </section>
      {advancedIndexes.length === 0 ? null : (
        <details className="advanced-sections">
          <summary>Connected services, add-ons, and abilities</summary>
          <p>Open these details when you need to inspect extensions or reusable tools.</p>
          <section className="surface-grid">
            {advancedIndexes.map(renderCard)}
          </section>
        </details>
      )}
    </div>
  )
}

function BrowserMailboxGuide({
  pending,
  value,
}: {
  pending: boolean
  value: unknown
}) {
  const record = isRecord(value) ? value : undefined
  const tools = Array.isArray(record?.tools) ? record.tools : []
  const findTool = (name: string) =>
    tools.find((tool) => isRecord(tool) && tool.name === name)
  const browser = findTool('browser_navigate')
  const mailbox = findTool('agent_mailbox_status')
  const browserReady = isRecord(browser) && browser.ready === true
  const mailboxReady = isRecord(mailbox) && mailbox.ready === true
  const ready = browserReady && mailboxReady
  return (
    <section className={`connection-setup-note ${ready ? 'ready' : ''}`}>
      <span className="connection-note-icon" aria-hidden="true">
        <Icon name={ready ? 'check' : 'workflow'} />
      </span>
      <div>
        <span className="operation-kicker">BROWSER WORKFLOWS</span>
        <strong>
          {pending
            ? 'Checking browser and agent email'
            : ready
              ? 'Browser workflows and private verification are ready'
              : browserReady
                ? 'Browser control is ready; agent email needs setup'
                : 'Browser workflow setup is incomplete'}
        </strong>
        <small>
          {ready
            ? 'Ion can use ordinary websites and privately move confirmation codes into an approved browser field.'
            : 'Install Chromium and connect machine-mail. Passwords and verification codes stay on the server and never appear on this page.'}
        </small>
        {ready ? null : (
          <details className="technical-details">
            <summary>Protected setting names</summary>
            <code>ION_BROWSER_EXECUTABLE · MACHINE_MAIL_ADDRESS · MACHINE_MAIL_API_KEY</code>
          </details>
        )}
      </div>
    </section>
  )
}

function ProviderConnectionGuide({
  error,
  pending,
  value,
}: {
  error: Error | null
  pending: boolean
  value: unknown
}) {
  const providers = Array.isArray(value) ? value : []
  const provider = isRecord(providers[0]) ? providers[0] : undefined
  const ready = provider !== undefined
  return (
    <section className={`connection-setup-note ${ready ? 'ready' : ''}`}>
      <span className="connection-note-icon" aria-hidden="true">
        <Icon name={ready ? 'check' : 'workflow'} />
      </span>
      <div>
        <span className="operation-kicker">PRIMARY MODEL</span>
        <strong>
          {pending
            ? 'Checking the model connection'
            : error !== null
              ? 'The model connection could not be checked'
              : ready
                ? `${String(provider.name ?? 'Your model')} is configured`
                : 'Connect a model before starting agent work'}
        </strong>
        <small>
          {ready
            ? `${String(provider.model ?? 'Selected model')} is used for conversations. Its private key never reaches this page.`
            : 'Add the provider name, server address, private key, and model name to the protected dashboard settings, then restart Ion.'}
        </small>
        {ready ? null : (
          <details className="technical-details">
            <summary>Technical setting names</summary>
            <code>PROVIDER_NAME · PROVIDER_BASE_URL · PROVIDER_API_KEY · LLM_MODEL</code>
          </details>
        )}
      </div>
    </section>
  )
}

function PluginSandbox() {
  const document = useMemo(
    () =>
      '<!doctype html><meta charset="utf-8"><style>body{margin:0;background:#171816;color:#b8b5ad;font:12px system-ui;padding:14px}strong{color:#e9e5dc;display:block;margin-bottom:6px}</style><strong>Isolated add-on preview</strong>This preview cannot run scripts, submit forms, open windows, or access Ion.',
    [],
  )
  return (
    <section className="plugin-sandbox">
      <div>
        <span className="operation-kicker">PREVIEW</span>
        <strong>Extension preview</strong>
        <small>This content is isolated so an add-on cannot access Ion or act without permission.</small>
      </div>
      <iframe sandbox="" srcDoc={document} title="Sandboxed plugin surface" />
    </section>
  )
}

type SurfaceState = 'pending' | 'error' | 'empty' | 'ready'

export function surfaceState(
  isPending: boolean,
  isError: boolean,
  value: unknown,
): SurfaceState {
  if (isPending) return 'pending'
  if (isError || projectionProblem(value)?.kind === 'error') return 'error'
  if (projectionProblem(value)?.kind === 'empty' || isEmptyValue(value)) return 'empty'
  return 'ready'
}

function projectionProblem(
  value: unknown,
): { kind: 'error' | 'empty'; reason: string | undefined } | undefined {
  if (!isRecord(value) || typeof value.status !== 'string') return undefined
  const reason = typeof value.reason === 'string' ? value.reason : undefined
  if (value.status === 'unavailable') return { kind: 'error', reason }
  if (value.status === 'not_available') return { kind: 'empty', reason }
  return undefined
}

function isEmptyValue(value: unknown): boolean {
  if (value === null || value === undefined) return true
  if (Array.isArray(value)) return value.length === 0
  if (!isRecord(value)) return value === ''
  if (Object.keys(value).length === 0) return true
  return Array.isArray(value.memories) && value.memories.length === 0
}

function statusLabel(state: SurfaceState): string {
  return {
    pending: 'Checking',
    error: 'Unavailable',
    empty: 'No data yet',
    ready: 'Available',
  }[state]
}

function pluralize(word: string, count: number): string {
  return count === 1 ? word : `${word}s`
}

function friendlyProblem(operation: Operation, reason?: string): string {
  if (reason?.includes('no cognition checkpoint')) {
    return 'Start a conversation first. Ion will show this after it has something to work with.'
  }
  const messages: Partial<Record<Operation, string>> = {
    'memory.search':
      'Ion could not read saved knowledge right now. Your encrypted records were not changed.',
    'provider.list':
      'Model connection information could not be checked. Existing conversations are unaffected.',
    'channel.health':
      'Delivery connections could not be checked. Try again before relying on an external channel.',
    'system.health':
      'The service did not answer its health check. Try again shortly.',
  }
  return messages[operation] ??
    'Ion could not check this section right now. Try again shortly.'
}

function emptyState(operation: Operation): { title: string; description: string } {
  const messages: Partial<
    Record<Operation, { title: string; description: string }>
  > = {
    'automatrix.list': {
      title: 'No background tasks are waiting',
      description: 'Ion has not proposed any unattended work for your review.',
    },
	'artifact.list': {
	  title: 'No deliverables recorded',
	  description: 'Ion will record and independently verify files that support completion criteria.',
	},
    'curiosity.targets': {
      title: 'No open questions right now',
      description: 'Ion has not found a recurring knowledge gap worth exploring.',
    },
    'dreamweaver.beliefs': {
      title: 'No derived ideas yet',
      description: 'New ideas appear here only after enough independent knowledge supports them.',
    },
    'logs.query': {
      title: 'No recent activity to show',
      description: 'New system activity will appear here as it happens.',
    },
    'memory.search': {
      title: 'No saved knowledge yet',
      description:
        'Conversation history is still kept privately. Explicitly saved facts and preferences will appear here.',
    },
    'plugin.list': {
      title: 'No add-ons installed',
      description: 'Installed extensions will appear here with their permissions and status.',
    },
    'provider.list': {
      title: 'No model connected',
      description: 'Connect a model before asking Ion to do agent work.',
    },
    'provider.usage': {
      title: 'Usage accounting is unavailable',
      description: 'Ion will show real request and token totals when the live model reports them.',
    },
    'premise.list': {
      title: 'No working assumptions',
      description: 'Assumptions appear when Ion needs to reason beyond confirmed facts.',
    },
    'schedule.list': {
      title: 'No scheduled work',
      description: 'Recurring and future work will appear here.',
    },
    'skill.list': {
      title: 'No saved abilities',
      description: 'Reusable procedures will appear here after they are installed or learned.',
    },
    'skill.lifecycle': {
      title: 'No ability history',
      description: 'Imported, proposed, adopted, rejected, and retired ability versions will appear here.',
    },
    'swarm.list': {
      title: 'No helpers are active',
      description: 'Additional agents will appear here while they are helping with a task.',
    },
    'supervisor.list': {
      title: 'No supervised work is active',
      description: 'Accepted outcomes will appear here when Ion divides them into specialist workstreams.',
    },
    'taskgraph.todo': {
      title: 'No next steps are waiting',
      description: 'This conversation has no unfinished plan steps.',
    },
  }
  return messages[operation] ?? {
    title: 'Nothing to show yet',
    description: 'Information will appear here when it becomes available.',
  }
}

function TechnicalDetails({
  operation,
  revision,
  value,
}: {
  operation: Operation
  revision: number | undefined
  value: unknown
}) {
  return (
    <details className="technical-details">
      <summary>Technical details</summary>
      <dl>
        <div>
          <dt>Data source</dt>
          <dd><code>{operation}</code></dd>
        </div>
        <div>
          <dt>Version</dt>
          <dd>{revision ?? '—'}</dd>
        </div>
      </dl>
      <span>Raw response</span>
      <pre><code>{pretty(value)}</code></pre>
    </details>
  )
}

export function StructuredValue({
  onSupervisorCommand,
  operation,
  value,
}: {
  onSupervisorCommand?: SupervisorCommand | undefined
  operation?: Operation
  value: unknown
}) {
  if (operation === 'liveness.get' && isRecord(value)) {
    return <LivenessView value={value} />
  }
  if (operation === 'continuity.brief' && isRecord(value)) {
    return <ReturnBriefView value={value} />
  }
	if (operation === 'work.brief' && isRecord(value)) {
	  return <WorkBriefView value={value} />
	}
	if (operation === 'autonomy.get' && isRecord(value)) {
	  return <AutonomyView value={value} />
	}
	if (operation === 'studio.intent.list' && isRecord(value)) {
	  return <StudioIntentView value={value} />
	}
	if (operation === 'supervisor.get' && isRecord(value)) {
	  return <SupervisorRunView value={value} />
	}
  if (operation === 'soul.get' && isRecord(value)) {
    return <SoulView value={value} />
  }
  if (
    operation === 'memory.search' &&
    isRecord(value) &&
    Array.isArray(value.memories)
  ) {
    return (
      <>
        <ReadableList operation={operation} value={value.memories} />
        {value.truncated === true ? (
          <p className="result-note">Showing the first matching saved items.</p>
        ) : null}
      </>
    )
  }
  if (Array.isArray(value)) {
    if (operation === 'channel.list') return <ChannelList value={value} />
    if (operation === 'automatrix.list') return <AutomatrixList value={value} />
    if (operation === 'supervisor.list') {
      return (
        <SupervisorListView
          onSupervisorCommand={onSupervisorCommand}
          value={value}
        />
      )
    }
    return <ReadableList operation={operation} value={value} />
  }
  if (isRecord(value)) {
    return <ReadableRecord operation={operation} value={value} depth={0} />
  }
  return <div className="surface-value">{formatScalar(value)}</div>
}

type SupervisorCommand = (
  operation: 'supervisor.steer' | 'supervisor.cancel',
  payload: Record<string, unknown>,
) => Promise<void>

function SupervisorListView({
  onSupervisorCommand,
  value,
}: {
  onSupervisorCommand?: SupervisorCommand | undefined
  value: unknown[]
}) {
  const runs = value.filter(isRecord)
  if (runs.length === 0) {
    return (
      <div className="surface-empty">
        <strong>No supervised work is active</strong>
        <span>When Ion splits an accepted outcome into specialist workstreams, live progress will appear here.</span>
      </div>
    )
  }
  return (
    <div className="supervisor-runs">
      {runs.slice(0, 4).map((run, index) => (
        <SupervisorRunView
          key={stableKey(run, index)}
          onSupervisorCommand={onSupervisorCommand}
          value={run}
        />
      ))}
    </div>
  )
}

function SupervisorRunView({
  onSupervisorCommand,
  value,
}: {
  onSupervisorCommand?: SupervisorCommand | undefined
  value: Record<string, unknown>
}) {
  const [instruction, setInstruction] = useState('')
  const [commandError, setCommandError] = useState<string>()
  const [commandPending, setCommandPending] = useState(false)
  const tasks = Array.isArray(value.tasks) ? value.tasks.filter(isRecord) : []
  const budget = isRecord(value.budget) ? value.budget : {}
  const usage = isRecord(value.usage) ? value.usage : {}
  const running = tasks.filter((task) => task.status === 'running').length
  const completed = tasks.filter((task) => task.status === 'completed').length
  const attention = tasks.filter((task) =>
    task.status === 'blocked' || task.status === 'outcome_unknown' || task.status === 'waiting_evidence',
  ).length
  const runID = typeof value.id === 'string' ? value.id : ''
  const status = String(value.status ?? 'waiting')
  const active = !['cancelled', 'completed'].includes(status)
  const command = async (
    operation: 'supervisor.steer' | 'supervisor.cancel',
  ) => {
    if (onSupervisorCommand === undefined || runID === '') return
    setCommandPending(true)
    setCommandError(undefined)
    try {
      const payload: Record<string, unknown> = { run_id: runID }
      if (operation === 'supervisor.steer') {
        payload.instruction = instruction.trim()
      }
      await onSupervisorCommand(operation, payload)
      if (operation === 'supervisor.steer') setInstruction('')
    } catch (error) {
      setCommandError(
        error instanceof Error ? error.message : 'The supervisor command failed.',
      )
    } finally {
      setCommandPending(false)
    }
  }
  return (
    <section className="supervisor-run" data-supervisor-id={runID || undefined}>
      <header>
        <div>
          <span className="eyebrow">AGENT SUPERVISOR</span>
          <h3>{humanize(String(value.status ?? 'waiting'))}</h3>
        </div>
        <span className="supervisor-count">{running} active / {Number(budget.max_parallel ?? 0)} lanes</span>
      </header>
      <p>{tasks.length} workstreams · {completed} completed · {attention} need attention</p>
      {attention > 0 ? (
        <p className="supervisor-attention" role="status">
          {attention} {pluralize('workstream', attention)} need a decision or verified evidence.
        </p>
      ) : null}
      <div className="supervisor-usage">
        <span>{Number(usage.tokens ?? 0).toLocaleString()} tokens</span>
        <span>{Number(usage.tool_calls ?? 0)} actions</span>
        <span>{Number(usage.cost_cents ?? 0)}¢ model cost</span>
        <span>{Number(usage.provider_cents ?? 0)}¢ provider spend</span>
      </div>
      <ol className="supervisor-task-grid" aria-label="Specialist progress">
        {tasks.map((task, index) => {
          const packet = isRecord(task.packet) ? task.packet : {}
          const progress = Math.max(0, Math.min(100, Number(task.progress ?? 0)))
          const attempts = Array.isArray(task.attempts) ? task.attempts.length : 0
          return (
            <li className="supervisor-task" key={stableKey(task, index)}>
              <div>
                <strong>{formatScalar(packet.title)}</strong>
                <span>{humanize(String(packet.specialist ?? 'specialist'))} · {humanize(String(task.status ?? 'pending'))}</span>
              </div>
              <progress aria-label={`${formatScalar(packet.title)} progress`} max={100} value={progress} />
              <span>{progress}% · {attempts} {pluralize('attempt', attempts)}</span>
              {typeof task.blocking_reason === 'string' && task.blocking_reason !== '' ? (
                <p className="result-note">{task.blocking_reason}</p>
              ) : null}
            </li>
          )
        })}
      </ol>
      {onSupervisorCommand !== undefined && active && runID !== '' ? (
        <form
          className="supervisor-controls"
          onSubmit={(event) => {
            event.preventDefault()
            if (instruction.trim() !== '') void command('supervisor.steer')
          }}
        >
          <label>
            Guidance for this outcome
            <input
              maxLength={4096}
              onChange={(event) => setInstruction(event.target.value)}
              placeholder="Add decision-changing guidance"
              value={instruction}
            />
          </label>
          <div className="inline-actions">
            <button
              disabled={commandPending || instruction.trim() === ''}
              type="submit"
            >
              Add guidance
            </button>
            <button
              className="quiet-button danger-button"
              disabled={commandPending}
              onClick={() => void command('supervisor.cancel')}
              type="button"
            >
              Cancel supervised work
            </button>
          </div>
          {commandError === undefined ? null : (
            <p className="result-note" role="alert">{commandError}</p>
          )}
        </form>
      ) : null}
      <p className="result-note">Results merge in dependency and task order. Safety, approvals, and evidence gates remain active for every specialist.</p>
    </section>
  )
}

function WorkBriefView({ value }: { value: Record<string, unknown> }) {
  const contract = isRecord(value.contract) ? value.contract : undefined
  const verified = Array.isArray(value.verified_criteria) ? value.verified_criteria : []
  const unverified = Array.isArray(value.unverified_criteria) ? value.unverified_criteria : []
  const deliverables = Array.isArray(value.deliverables) ? value.deliverables.filter(isRecord) : []
  if (contract === undefined) {
    return (
      <div className="surface-empty">
        <strong>No outcome has been set for this work</strong>
        <span>Ask Ion to define the deliverable, done criteria, verification, and next action before substantial work begins.</span>
      </div>
    )
  }
  return (
    <div className="liveness-summary">
      <section>
        <h3>Outcome</h3>
        <p className="record-title">{formatScalar(contract.goal)}</p>
        <p>{formatScalar(contract.deliverable)}</p>
      </section>
      <section>
        <h3>What happens next</h3>
        <p>{formatScalar(value.next_action)}</p>
        {typeof value.blocking_reason === 'string' && value.blocking_reason !== '' ? (
          <p className="result-note">Waiting: {value.blocking_reason}</p>
        ) : null}
      </section>
      <section>
        <h3>Proof of completion</h3>
        <p>{Number(value.completion_percentage ?? 0)}% verified · {verified.length} passed · {unverified.length} still need evidence</p>
        {unverified.length === 0 ? null : (
		  <p className="result-note">Still unverified: {unverified.map((item) => formatScalar(item)).join(', ')}</p>
        )}
      </section>
      <section>
        <h3>Deliverables</h3>
        {deliverables.length === 0 ? (
          <p className="result-note">No deliverables have been recorded yet.</p>
        ) : <ReadableList operation="artifact.list" value={deliverables} />}
      </section>
    </div>
  )
}

function SoulView({ value }: { value: Record<string, unknown> }) {
  const current = isRecord(value.current) ? value.current : undefined
  const history = Array.isArray(value.history) ? value.history : []
  const proposals = Array.isArray(value.proposals) ? value.proposals : []
  const pending = Array.isArray(value.pending_proposals) ? value.pending_proposals : []
  if (current === undefined) {
    return (
      <div className="surface-empty">
        <strong>No approved identity record</strong>
        <span>Ion has not loaded a signed set of guiding values yet.</span>
      </div>
    )
  }
  return (
    <div className="liveness-summary identity-summary">
      <section>
        <h3>Current guiding values</h3>
        <p className="record-title">{formatScalar(current.content)}</p>
        <dl className="record-facts">
          <div>
            <dt>Approved</dt>
            <dd>{formatScalar(current.approved_at, 'approved_at')}</dd>
          </div>
          <div>
            <dt>Revision</dt>
            <dd>{formatScalar(current.number)}</dd>
          </div>
          <div>
            <dt>Provenance</dt>
            <dd>{formatScalar(current.provenance)}</dd>
          </div>
        </dl>
      </section>
      <section>
        <h3>Review state</h3>
        <p>
          {history.length} approved {pluralize('version', history.length)} · {pending.length} pending · {proposals.length} total {pluralize('proposal', proposals.length)}
        </p>
        <p className="result-note">The signed content and hashes remain available in Technical details.</p>
      </section>
    </div>
  )
}

function AutonomyView({ value }: { value: Record<string, unknown> }) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [notice, setNotice] = useState<string>()
  const update = async (mode: 'off' | 'suggest' | 'approved', paused: boolean) => {
    const response = await operator.command(
      'autonomy.update',
      {
        mode,
        paused,
        max_tool_calls: Number(value.max_tool_calls ?? 20),
        max_tokens: Number(value.max_tokens ?? 64000),
        max_elapsed_seconds: Number(value.max_elapsed_seconds ?? 1800),
        max_errors: Number(value.max_errors ?? 3),
        cooldown_seconds: Number(value.cooldown_seconds ?? 300),
      },
      crypto.randomUUID(),
      operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
    )
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    setNotice(paused ? 'Background work paused.' : `Autonomy set to ${mode}.`)
    await queryClient.invalidateQueries({ queryKey: ['surface', 'autonomy.get'] })
    await queryClient.invalidateQueries({ queryKey: ['surface', 'work.brief'] })
  }
  const mode = value.mode === 'off' || value.mode === 'approved' ? value.mode : 'suggest'
  const paused = value.paused === true
  return (
    <div className="liveness-summary">
      <section>
        <h3>Background initiative</h3>
        <p>{paused ? 'Paused' : mode === 'approved' ? 'May run only explicitly approved plans' : mode === 'off' ? 'Off' : 'Suggestions only'}</p>
        <div className="inline-actions">
          <button className={mode === 'suggest' && !paused ? '' : 'secondary'} onClick={() => { void update('suggest', false) }} type="button">Suggest only</button>
          <button className={mode === 'approved' && !paused ? '' : 'secondary'} onClick={() => { void update('approved', false) }} type="button">Approved plans</button>
          <button className="secondary" onClick={() => { void update(mode, !paused) }} type="button">{paused ? 'Resume' : 'Pause'}</button>
          <button className="secondary" onClick={() => { void update('off', false) }} type="button">Turn off</button>
        </div>
        {notice === undefined ? null : <p className="result-note" role="status">{notice}</p>}
      </section>
      <section>
        <h3>Hard limits</h3>
        <p>{Number(value.max_tool_calls ?? 0)} actions · {Number(value.max_tokens ?? 0)} tokens · {Number(value.max_elapsed_seconds ?? 0)} seconds · {Number(value.max_errors ?? 0)} errors</p>
        <p className="result-note">Safety policy, approvals, and required verification always remain in force.</p>
      </section>
    </div>
  )
}

function StudioIntentView({ value }: { value: Record<string, unknown> }) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [notice, setNotice] = useState<string>()
	const [decisions, setDecisions] = useState<Record<string, string>>({})
  const intents = Array.isArray(value.intents) ? value.intents.filter(isRecord) : []
  const decide = async (intentID: string, proposalID: string, accept: boolean) => {
    const response = await operator.command(
      'studio.proposal.decide',
	  { intent_id: intentID, proposal_id: proposalID, accept, reason: accept ? 'Approved in Software Studio' : 'Rejected in Software Studio', assumption_decisions: decisions },
      crypto.randomUUID(),
      operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
    )
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    setNotice(accept ? 'Specification accepted. Review application before implementation.' : 'Specification rejected. No project files were changed.')
    await queryClient.invalidateQueries({ queryKey: ['surface', 'studio.intent.list'] })
  }
  const apply = async (intentID: string, proposalID: string) => {
    const response = await operator.command(
      'studio.proposal.apply',
      { intent_id: intentID, proposal_id: proposalID },
      crypto.randomUUID(),
      operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
    )
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    setNotice('The accepted change was written to the authoritative specification and generated views were refreshed.')
    await queryClient.invalidateQueries({ queryKey: ['surface', 'studio.intent.list'] })
  }
  if (intents.length === 0) {
    return (
      <div className="surface-empty">
        <strong>No software specification is waiting</strong>
        <span>Tell Ion what you want to build or change. He will show what he understood before implementation begins.</span>
      </div>
    )
  }
  return (
    <div className="liveness-summary">
      {intents.map((intent) => {
        const proposals = Array.isArray(intent.proposals) ? intent.proposals.filter(isRecord) : []
        const assumptions = Array.isArray(intent.assumptions) ? intent.assumptions.filter(isRecord) : []
        return (
          <section key={String(intent.id)}>
            <h3>{formatScalar(intent.goal)}</h3>
            <p>{assumptions.length} visible assumption{assumptions.length === 1 ? '' : 's'} · project revision {formatScalar(intent.baseline_workspace_revision)}</p>
            {assumptions.map((assumption) => (
			  <div key={String(assumption.id)}>
			    <p className="result-note">
			      {assumption.material === true ? 'Decision needed' : 'Reversible assumption'}: {formatScalar(assumption.statement)}
			    </p>
			    {assumption.material === true && assumption.resolution === undefined ? (
			      <label>
			        {formatScalar(assumption.decision_needed)}
			        <input
			          onChange={(event) => setDecisions((current) => ({ ...current, [String(assumption.id)]: event.target.value }))}
			          value={decisions[String(assumption.id)] ?? ''}
			        />
			      </label>
			    ) : null}
			  </div>
            ))}
            {proposals.map((proposal) => {
              const delta = isRecord(proposal.delta) ? proposal.delta : {}
              const criteria = Array.isArray(delta.acceptance_criteria) ? delta.acceptance_criteria.filter(isRecord) : []
              const behavior = Array.isArray(delta.user_visible_behavior) ? delta.user_visible_behavior : []
              const risks = Array.isArray(delta.risks) ? delta.risks : []
              const status = String(proposal.status ?? 'proposed')
              return (
                <div className="proposal-card" key={String(proposal.id)}>
                  <strong>Specification v{formatScalar(proposal.version)} · {humanize(status)}</strong>
                  <p>{formatScalar(proposal.rationale)}</p>
                  <h4>What will change</h4>
                  <ul>{behavior.map((item) => <li key={String(item)}>{formatScalar(item)}</li>)}</ul>
                  <h4>How success is checked</h4>
                  <ul>{criteria.map((criterion) => <li key={String(criterion.id)}>{formatScalar(criterion.description)}</li>)}</ul>
                  {risks.length === 0 ? null : <p className="result-note">Risks: {risks.map((risk) => formatScalar(risk)).join(', ')}</p>}
                  <div className="inline-actions">
                    {status === 'proposed' ? <>
                      <button type="button" onClick={() => { void decide(String(intent.id), String(proposal.id), true) }}>Accept specification</button>
                      <button className="secondary" type="button" onClick={() => { void decide(String(intent.id), String(proposal.id), false) }}>Reject</button>
                    </> : null}
                    {status === 'accepted' && proposal.applied_at === undefined ? (
                      <button type="button" onClick={() => { void apply(String(intent.id), String(proposal.id)) }}>Apply to authoritative spec</button>
                    ) : null}
                    {proposal.applied_at === undefined ? null : <span role="status">Applied to spec</span>}
                  </div>
                </div>
              )
            })}
          </section>
        )
      })}
      {notice === undefined ? null : <p className="result-note" role="status">{notice}</p>}
    </div>
  )
}

function AutomatrixList({ value }: { value: unknown[] }) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [notice, setNotice] = useState<string>()
  const decide = async (
    item: Record<string, unknown>,
    operation: 'automatrix.approve' | 'automatrix.reject',
  ) => {
    if (typeof item.id !== 'string') return
    const response = await operator.command(
      operation,
      operation === 'automatrix.approve'
        ? { item_id: item.id, actions: Array.isArray(item.actions) ? item.actions : [] }
        : { item_id: item.id },
      crypto.randomUUID(),
      operator.sessionID === undefined ? {} : { session_id: operator.sessionID },
    )
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    setNotice(operation === 'automatrix.approve'
      ? 'Background investigation approved.'
      : 'Background proposal dismissed.')
    await queryClient.invalidateQueries({ queryKey: ['surface', 'automatrix.list'] })
  }
  return (
    <div>
      {notice === undefined ? null : <p className="result-note" role="status">{notice}</p>}
      <ol className="readable-list">
        {value.filter(isRecord).slice(0, 8).map((item, index) => {
          const canApprove = Array.isArray(item.actions) && item.actions.length > 0
          return (
            <li className="readable-record" key={stableKey(item, index)}>
              <span className="record-number">{index + 1}</span>
              <div>
                <p className="record-title">{formatScalar(item.description)}</p>
                <p>
                  {canApprove
                    ? 'Proposed as a bounded private investigation. Nothing runs until you approve it.'
                    : 'This opportunity needs a safe action plan before it can run.'}
                </p>
                <div className="inline-actions">
                  <button
                    disabled={!canApprove}
                    onClick={() => { void decide(item, 'automatrix.approve') }}
                    type="button"
                  >
                    Approve investigation
                  </button>
                  <button
                    className="secondary"
                    onClick={() => { void decide(item, 'automatrix.reject') }}
                    type="button"
                  >
                    Dismiss
                  </button>
                </div>
              </div>
            </li>
          )
        })}
      </ol>
    </div>
  )
}

function ReturnBriefView({ value }: { value: Record<string, unknown> }) {
  const sections = Array.isArray(value.sections)
    ? value.sections.filter(isRecord)
    : []
  if (value.status === 'no_activity' || sections.length === 0) {
    return (
      <div className="liveness-summary">
        <p className="result-note">
          No verified work, failure, decision, file change, pending question, discovery, repair,
          proposal, incomplete work, or deadline was recorded in this period.
        </p>
      </div>
    )
  }
  return (
    <div className="liveness-summary">
      {value.status === 'partial' ? (
        <p className="result-note" role="status">
          This summary is partial because earlier retained events are no longer available.
        </p>
      ) : null}
      {sections.map((section, sectionIndex) => {
        const items = Array.isArray(section.items)
          ? section.items.filter(isRecord)
          : []
        return (
          <section key={stableKey(section, sectionIndex)}>
            <h3>{formatScalar(section.label)}</h3>
            <ol className="readable-list">
              {items.map((item, index) => (
                <li className="readable-record" key={stableKey(item, index)}>
                  <span className="record-number">{index + 1}</span>
                  <div>
                    <p className="record-title">{formatScalar(item.summary)}</p>
                    <p>{formatTimestampValue(item.occurred_at)}</p>
                  </div>
                </li>
              ))}
            </ol>
          </section>
        )
      })}
    </div>
  )
}

function LivenessView({ value }: { value: Record<string, unknown> }) {
  const presence = isRecord(value.presence) ? value.presence : undefined
  const sinceAway = presence !== undefined && Array.isArray(presence.since_you_were_away)
    ? presence.since_you_were_away.filter(isRecord)
    : []
  const morningBrief = presence !== undefined && isRecord(presence.morning_brief)
    ? presence.morning_brief
    : undefined
  const briefItems = morningBrief !== undefined && Array.isArray(morningBrief.items)
    ? morningBrief.items.filter(isRecord)
    : []
  const decision = isRecord(value.decision) ? value.decision : undefined
  const causes = decision !== undefined && Array.isArray(decision.causes)
    ? decision.causes.filter(isRecord)
    : []
  const aesthetic = isRecord(value.aesthetic) ? value.aesthetic : undefined
  const repair = isRecord(value.repair) ? value.repair : undefined
  const relationships = Array.isArray(value.relationships)
    ? value.relationships.filter(isRecord)
    : []
  return (
    <div className="liveness-summary">
      <section>
        <h3>Since you were away</h3>
        {sinceAway.length === 0 ? (
          <p className="result-note">No verified background activity has been recorded for you.</p>
        ) : (
          <ol className="readable-list">
            {sinceAway.slice(0, 6).map((item, index) => (
              <li className="readable-record" key={stableKey(item, index)}>
                <span className="record-number">{index + 1}</span>
                <div>
                  <p className="record-title">{formatScalar(item.kind)}</p>
                  <p>{formatScalar(item.summary)}</p>
                </div>
              </li>
            ))}
          </ol>
        )}
      </section>
      {morningBrief === undefined ? null : (
        <section>
          <h3>Latest morning brief</h3>
          <ReadableList operation="liveness.get" value={briefItems} />
        </section>
      )}
      <section>
        <h3>What changed in this decision</h3>
        {causes.length === 0 ? (
          <p className="result-note">No state-driven change is active for this session yet.</p>
        ) : (
          <ul className="liveness-causes">
            {causes.map((cause, index) => (
              <li key={stableKey(cause, index)}>{formatScalar(cause.explanation)}</li>
            ))}
          </ul>
        )}
      </section>
      {aesthetic === undefined ? null : (
        <section>
          <h3>Confirmed working taste</h3>
          <p>{formatScalar(aesthetic.label)}</p>
        </section>
      )}
      {repair === undefined ? null : (
        <section>
          <h3>Repair carried forward</h3>
          <p>{formatScalar(repair.lesson)}</p>
        </section>
      )}
      {relationships.length === 0 ? null : (
        <section>
          <h3>How you prefer to work together</h3>
          <RelationshipProfileControls relationships={relationships} />
        </section>
      )}
    </div>
  )
}

function RelationshipProfileControls({
  relationships,
}: {
  relationships: Record<string, unknown>[]
}) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const initialDomain = typeof relationships[0]?.domain === 'string'
    ? relationships[0].domain
    : 'general'
  const [domain, setDomain] = useState(initialDomain)
  const [notice, setNotice] = useState<string>()
  const current = relationships.find((item) => item.domain === domain)
  const profile = current !== undefined && isRecord(current.profile)
    ? current.profile
    : {}
  const scope = operator.sessionID === undefined ? {} : { session_id: operator.sessionID }
  const send = async (payload: Record<string, unknown>, success: string) => {
    const response = await operator.command(
      'relationship.update',
      { domain, ...payload },
      crypto.randomUUID(),
      scope,
    )
    if (response.error !== undefined) {
      setNotice(response.error.message)
      return
    }
    setNotice(success)
    await queryClient.invalidateQueries({ queryKey: ['surface', 'liveness.get'] })
  }
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const patch: Record<string, unknown> = {}
    const stringField = (name: string) => {
      const value = String(data.get(name) ?? '').trim()
      if (value !== '') patch[name] = value
    }
    stringField('response_length')
    stringField('directness')
    stringField('domain_expertise')
    stringField('risk_tolerance')
    stringField('notification_cadence')
    for (const name of ['conclusion_first', 'proactive_suggestions']) {
      const value = String(data.get(name) ?? '')
      if (value === 'true' || value === 'false') patch[name] = value === 'true'
    }
    for (const name of ['preferred_tools', 'dislikes', 'constraints', 'project_principles']) {
      const values = String(data.get(name) ?? '')
        .split(/[\n,]/)
        .map((value) => value.trim())
        .filter((value) => value !== '')
      if (values.length > 0) patch[name] = values
    }
    await send({ action: 'correct', patch }, 'Relationship preferences saved.')
  }
  const remove = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const field = String(new FormData(event.currentTarget).get('field') ?? '')
    await send({ action: 'remove', fields: [field] }, 'Preference removed.')
  }
  const pinned = Array.isArray(profile.pinned_fields)
    ? profile.pinned_fields.map((value) => String(value))
    : []
  return (
    <div className="relationship-profile">
      {relationships.length === 0 ? (
        <p className="result-note">No relationship profile exists yet. Saving a preference creates one for this domain.</p>
      ) : (
        <label>
          Work area
          <select onChange={(event) => setDomain(event.target.value)} value={domain}>
            {relationships.map((item) => {
              const value = String(item.domain ?? 'general')
              return <option key={value} value={value}>{humanize(value)}</option>
            })}
          </select>
        </label>
      )}
      <dl className="readable-definition-list">
        <div><dt>Response length</dt><dd>{formatScalar(profile.response_length ?? current?.communication_preference ?? 'Not set')}</dd></div>
        <div><dt>Directness</dt><dd>{formatScalar(profile.directness ?? 'Not set')}</dd></div>
        <div><dt>Conclusion first</dt><dd>{formatScalar(profile.conclusion_first ?? 'Not set')}</dd></div>
        <div><dt>Expertise</dt><dd>{formatScalar(current?.expertise ?? 'Not set')}</dd></div>
        <div><dt>Preferred tools</dt><dd>{formatScalar(profile.preferred_tools ?? 'Not set')}</dd></div>
        <div><dt>Risk tolerance</dt><dd>{formatScalar(profile.risk_tolerance ?? 'Not set')}</dd></div>
        <div><dt>Proactive suggestions</dt><dd>{formatScalar(profile.proactive_suggestions ?? 'Not set')}</dd></div>
        <div><dt>Notification cadence</dt><dd>{formatScalar(profile.notification_cadence ?? 'Not set')}</dd></div>
        <div><dt>Dislikes</dt><dd>{formatScalar(profile.dislikes ?? 'Not set')}</dd></div>
        <div><dt>Constraints</dt><dd>{formatScalar(profile.constraints ?? 'Not set')}</dd></div>
        <div><dt>Project principles</dt><dd>{formatScalar(profile.project_principles ?? 'Not set')}</dd></div>
        <div><dt>Pinned</dt><dd>{pinned.length === 0 ? 'None' : pinned.map(humanize).join(', ')}</dd></div>
      </dl>
      <details>
        <summary>Correct preferences</summary>
        <form className="compact-control-form" key={`${domain}-${String(current?.updated_at ?? '')}`} onSubmit={(event) => { void save(event) }}>
          <label>Response length<select defaultValue={String(profile.response_length ?? '')} name="response_length"><option value="">No change</option><option value="brief">Brief</option><option value="balanced">Balanced</option><option value="detailed">Detailed</option></select></label>
          <label>Directness<select defaultValue={String(profile.directness ?? '')} name="directness"><option value="">No change</option><option value="gentle">Gentle</option><option value="balanced">Balanced</option><option value="direct">Direct</option></select></label>
          <label>Conclusion first<select defaultValue="" name="conclusion_first"><option value="">No change</option><option value="true">Yes</option><option value="false">No</option></select></label>
          <label>Domain expertise<select defaultValue="" name="domain_expertise"><option value="">No change</option><option value="beginner">Beginner</option><option value="intermediate">Intermediate</option><option value="expert">Expert</option></select></label>
          <label>Preferred tools<input name="preferred_tools" placeholder="rg, go test" /></label>
          <label>Risk tolerance<select defaultValue={String(profile.risk_tolerance ?? '')} name="risk_tolerance"><option value="">No change</option><option value="low">Low</option><option value="moderate">Moderate</option><option value="high">High</option></select></label>
          <label>Proactive suggestions<select defaultValue="" name="proactive_suggestions"><option value="">No change</option><option value="true">Yes</option><option value="false">No</option></select></label>
          <label>Notification cadence<select defaultValue={String(profile.notification_cadence ?? '')} name="notification_cadence"><option value="">No change</option><option value="quiet">Quiet</option><option value="milestones">Milestones</option><option value="regular">Regular</option></select></label>
          <label>Dislikes<textarea name="dislikes" rows={2} /></label>
          <label>Constraints<textarea name="constraints" rows={2} /></label>
          <label>Project principles<textarea name="project_principles" rows={2} /></label>
          <button type="submit">Save preferences</button>
        </form>
      </details>
      <div className="inline-actions">
        <button className="secondary" onClick={() => { void send({ action: 'pin', fields: ['response_length', 'project_principles'] }, 'Important preferences pinned.') }} type="button">Pin important preferences</button>
        <button className="secondary" onClick={() => { void send({ action: 'propose_soul_v2' }, 'SOUL v2 proposal created for review. Nothing was applied.') }} type="button">Propose SOUL v2</button>
      </div>
      <form className="compact-control-form" onSubmit={(event) => { void remove(event) }}>
        <label>Remove a preference<select name="field"><option value="response_length">Response length</option><option value="directness">Directness</option><option value="conclusion_first">Conclusion first</option><option value="domain_expertise">Domain expertise</option><option value="preferred_tools">Preferred tools</option><option value="risk_tolerance">Risk tolerance</option><option value="proactive_suggestions">Proactive suggestions</option><option value="notification_cadence">Notification cadence</option><option value="dislikes">Dislikes</option><option value="constraints">Constraints</option><option value="project_principles">Project principles</option></select></label>
        <button className="secondary" type="submit">Remove selected preference</button>
      </form>
      {notice === undefined ? null : <p className="result-note" role="status">{notice}</p>}
    </div>
  )
}

function ReadableList({
  operation,
  value,
}: {
  operation: Operation | undefined
  value: unknown[]
}) {
  const visible = value.slice(0, 6)
  return (
    <>
      <ol className={`readable-list${visible.length > 3 ? ' readable-list-grid' : ''}`}>
        {visible.map((item, index) => {
          if (!isRecord(item)) {
            return (
              <li className="readable-record" key={stableKey(item, index)}>
                <span className="record-number">{index + 1}</span>
                <p>{formatScalar(item)}</p>
              </li>
            )
          }
          const primary = primaryRecordField(item)
          const facts = Object.entries(item)
            .filter(([key]) => key !== primary?.key && !isTechnicalField(key))
            .slice(0, 4)
          return (
            <li className="readable-record" key={stableKey(item, index)}>
              <span className="record-number">{index + 1}</span>
              <div>
                <p className="record-title">
                  {primary === undefined
                    ? `${operationTitle(operation ?? 'details')} item`
                    : formatScalar(primary.value, primary.key)}
                </p>
                {facts.length === 0 ? null : (
                  <dl className="record-facts">
                    {facts.map(([key, fact]) => (
                      <div key={key}>
                        <dt>{fieldLabel(key)}</dt>
                        <dd>{friendlyValue(fact, key, 2)}</dd>
                      </div>
                    ))}
                  </dl>
                )}
              </div>
            </li>
          )
        })}
      </ol>
      {value.length > visible.length ? (
        <p className="result-note">
          Showing {visible.length} of {value.length} items. Open Technical details for the complete response.
        </p>
      ) : null}
    </>
  )
}

function ReadableRecord({
  operation,
  value,
  depth,
}: {
  operation: Operation | undefined
  value: Record<string, unknown>
  depth: number
}) {
  const entries = Object.entries(value).filter(([key]) => !isTechnicalField(key))
  return (
    <dl className={`structured-data ${depth > 0 ? 'nested' : ''}`}>
      {entries.slice(0, 14).map(([key, item]) => (
        <div key={key}>
          <dt>{fieldLabel(key)}</dt>
          <dd>{friendlyValue(item, key, depth + 1, operation)}</dd>
        </div>
      ))}
    </dl>
  )
}

function friendlyValue(
  value: unknown,
  key: string,
  depth: number,
  operation?: Operation,
): ReactNode {
  if (Array.isArray(value)) {
    if (value.length === 0) return 'None'
    if (key === 'affected_subgoals') {
      return `${value.length} ${pluralize('plan area', value.length)}`
    }
    if (depth >= 2) return `${value.length} ${pluralize('item', value.length)}`
    if (value.every((item) => !isRecord(item))) {
      const visible = value.slice(0, 4).map((item) => formatScalar(item, key))
      return value.length > visible.length
        ? `${visible.join(', ')} · ${value.length - visible.length} more`
        : visible.join(', ')
    }
    return <ReadableList operation={operation} value={value} />
  }
  if (isRecord(value)) {
    if (depth >= 2) {
      const count = Object.keys(value).length
      return `${count} ${pluralize('detail', count)}`
    }
    return <ReadableRecord operation={operation} value={value} depth={depth} />
  }
  return formatScalar(value, key)
}

function primaryRecordField(
  value: Record<string, unknown>,
): { key: string; value: unknown } | undefined {
  for (const key of ['statement', 'content', 'title', 'name', 'label', 'description', 'operation']) {
    const item = value[key]
    if (typeof item === 'string' && item.trim() !== '') return { key, value: item }
  }
  const status = value.status
  if (typeof status === 'string') return { key: 'status', value: status }
  return undefined
}

function isTechnicalField(key: string): boolean {
  return key === 'id' ||
    key === 'hash' ||
    key.endsWith('_id') ||
    key.endsWith('_ids') ||
    key.endsWith('_hash') ||
    key === 'soul_hash' ||
    key === 'checkpoint' ||
    key === 'revision' ||
    key === 'sequence'
}

function fieldLabel(key: string): string {
  const labels: Record<string, string> = {
    active: 'Helpers working now',
    affected_subgoals: 'Plan areas affected',
    cache: 'Readiness checks',
    ciphertext_visible: 'Encrypted content exposed',
    configured: 'Connections set up',
    counts: 'Results by action',
    created_at: 'Created',
    credentials: 'Connection secret',
    edges: 'Connections',
    episodic: 'Recent experiences',
    failures: 'Failed requests',
    global_limit: 'Total helper limit',
    healthy: 'Connections working',
    load: 'Confidence',
    nodes: 'Saved items',
    parent_limit: 'Helpers per task',
    primary_external_channel: 'Main external channel',
    recursive_spawning: 'Helpers can create helpers',
    requests: 'Requests made',
    safety_boundary: 'Safety rules',
    semantic: 'Facts and meaning',
    session_limit: 'Helpers per conversation',
    source: 'Basis',
    statement: 'Assumption',
    threshold: 'Review after',
    tools: 'Action readiness',
    unavailable: 'Actions unavailable',
    verification: 'Integrity protection',
    verified: 'Check passed',
    working: 'In this conversation',
  }
  return labels[key] ?? humanize(key)
}

function formatScalar(value: unknown, key = ''): string {
  if (value === null || value === undefined || value === '') return 'Not set'
  if (typeof value === 'boolean') {
    if (key === 'ciphertext_visible') {
      return value ? 'Yes — needs attention' : 'No — content stays private'
    }
    return value ? 'Yes' : 'No'
  }
  if (typeof value === 'number') {
    if (
      value >= 0 &&
      value <= 1 &&
      ['confidence', 'curiosity', 'fatigue', 'frustration', 'load', 'satisfaction', 'trust', 'urgency'].includes(key)
    ) {
      return `${Math.round(value * 100)}%`
    }
    if (key === 'threshold') return `${value} repeated results`
    return new Intl.NumberFormat().format(value)
  }
  const text = String(value)
  if (typeof value === 'string' && /^[{[]/.test(text.trimStart())) {
    const summary = summarizeStructuredContent(text)
    if (summary !== undefined) return summary
  }
  if (key.endsWith('_at') && !Number.isNaN(Date.parse(text))) {
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(new Date(text))
  }
  if (key === 'source' && text === 'assumption') {
    return 'Ion inferred this; it is not confirmed yet'
  }
  if (key === 'credentials' && text === 'write-only') return 'Stored privately'
  if (key === 'status') return humanize(text)
  const memoryTypes: Record<string, string> = {
    '0x01': 'Identity',
    '0x02': 'Fact',
    '0x03': 'Preference',
    '0x04': 'Belief',
    '0x05': 'Event',
    '0x06': 'Goal',
    '0x07': 'Constraint',
    '0x08': 'Capability',
    '0x09': 'Pattern',
  }
  return compactDisplayText(memoryTypes[text] ?? text)
}

function formatTimestampValue(value: unknown): string {
  if (typeof value !== 'string') return 'Time unavailable'
  const parsed = new Date(value)
  return Number.isNaN(parsed.valueOf())
    ? 'Time unavailable'
    : parsed.toLocaleString()
}

function compactDisplayText(value: string): string {
  const normalized = value.replace(/\s+/g, ' ').trim()
  return normalized.length > 280 ? `${normalized.slice(0, 277)}…` : normalized
}

function summarizeStructuredContent(value: string): string | undefined {
  if (!value.trimStart().startsWith('{')) return undefined
  try {
    const decoded: unknown = JSON.parse(value)
    if (!isRecord(decoded)) return 'Structured saved record'
    if (typeof decoded.name === 'string' && decoded.name.trim() !== '') {
      const failed = (typeof decoded.failure_class === 'string' &&
        decoded.failure_class.trim() !== '') || decoded.error !== undefined
      return `${humanize(decoded.name)} ${failed ? 'failed' : 'completed'}`
    }
    for (const key of ['statement', 'title', 'label']) {
      if (typeof decoded[key] === 'string' && decoded[key].trim() !== '') {
        return decoded[key]
      }
    }
    return 'Structured saved record'
  } catch {
    const action = value.match(/"name"\s*:\s*"([a-zA-Z0-9_.-]+)"/)?.[1]
    return action === undefined
      ? 'Saved technical record'
      : `${humanize(action)} activity`
  }
}

function pretty(value: unknown): string {
  if (value === undefined) return 'No response payload.'
  return JSON.stringify(value, null, 2) ?? String(value)
}

function ChannelList({ value }: { value: unknown[] }) {
  return (
    <div className="channel-list">
      {value.map((item, index) => {
        if (!isRecord(item)) return <code key={index}>{compact(item)}</code>
        const setup = Array.isArray(item.setup)
          ? item.setup.filter((step): step is string => typeof step === 'string')
          : []
        return (
          <section className="channel-card" key={stableKey(item, index)}>
            <header>
              <strong>{renderValue(item.name)}</strong>
              <span className={`status-pill ${item.status === 'ready' ? '' : 'pending'}`}>
                {item.status === 'ready' ? 'Ready' : humanize(String(item.status ?? 'not configured'))}
              </span>
            </header>
            <p>
              {item.name === 'Telegram'
                ? 'Telegram is a first-class Ion chat: it uses the same agent, memory, tools, approvals, safety rules, and durable conversation path as this dashboard.'
                : 'The primary Ion conversation with encrypted history, live tools, approvals, and durable recovery.'}
            </p>
            {setup.length === 0 ? null : (
              <ol>
                {setup.map((step) => <li key={step}>{step}</li>)}
              </ol>
            )}
            <small>{renderValue(item.session)}</small>
          </section>
        )
      })}
    </div>
  )
}

function defaultPayload(
  operation: Operation,
  continuityPeriod: '24h' | '7d' | '30d' = '24h',
): Record<string, unknown> {
  if (operation === 'memory.search') return { limit: 24 }
  if (operation === 'logs.query') return { limit: 80 }
  if (operation === 'events.replay') return { after_sequence: 0, limit: 80 }
  if (operation === 'continuity.brief') return { period: continuityPeriod }
  return {}
}

function operationCategory(operation: string): string {
  const prefix = operation.split('.')[0] ?? operation
  return {
    automatrix: 'BACKGROUND WORK',
    artifact: 'DELIVERABLES',
    autonomy: 'AUTONOMY',
    channel: 'CHANNELS',
    continuity: 'CONTINUITY',
    commands: 'AVAILABLE ACTIONS',
    config: 'PREFERENCES',
    curiosity: 'OPEN QUESTIONS',
    dreamweaver: 'DERIVED IDEAS',
    integrity: 'SAFETY CHECKS',
    logs: 'ACTIVITY',
    mcp: 'CONNECTED SERVICES',
    memory: 'SAVED KNOWLEDGE',
    plugin: 'ADD-ONS',
    policy: 'DECISIONS',
    prediction: 'FORECASTS',
    premise: 'ASSUMPTIONS',
    provider: 'MODELS',
    receipt: 'OUTCOMES',
    schedule: 'SCHEDULES',
    skill: 'ABILITIES',
    soul: 'IDENTITY',
    swarm: 'HELPERS',
    supervisor: 'WIDE WORK',
    system: 'SYSTEM',
    taskgraph: 'GOALS',
    tool: 'ACTIONS',
    work: 'CURRENT OUTCOME',
    workflow: 'WORKFLOWS',
	studio: 'SOFTWARE STUDIO',
  }[prefix] ?? 'DETAILS'
}

function operationTitle(operation: string): string {
  const labels: Partial<Record<Operation, string>> = {
    'automatrix.list': 'Background tasks',
    'artifact.list': 'Deliverables and evidence',
    'autonomy.get': 'Autonomy and limits',
    'channel.health': 'Delivery health',
    'channel.list': 'Connected channels',
    'commands.catalog': 'What Ion can do',
    'config.get': 'Current preferences',
    'continuity.brief': 'Verified changes while you were away',
    'curiosity.targets': 'Questions worth exploring',
    'dreamweaver.beliefs': 'Ideas derived from existing knowledge',
    'integrity.latest': 'Latest safety check',
    'logs.query': 'Recent activity',
    'liveness.get': 'Living context',
    'mcp.servers': 'Connected services',
    'mcp.tools': 'Tools from connected services',
    'memory.activation': 'Knowledge currently in use',
    'memory.graph': 'Related knowledge',
    'memory.search': 'Saved knowledge',
    'plugin.list': 'Installed add-ons',
	'project.list': 'Software projects',
	'studio.intent.list': 'Specifications to review',
    'policy.events': 'Recent safety decisions',
    'prediction.list': 'Forecasts',
    'premise.list': 'Assumptions',
    'provider.list': 'Connected model',
    'provider.usage': 'Model usage',
    'receipt.list': 'Recent outcomes',
    'receipt.verify': 'Outcome verification',
    'schedule.list': 'Scheduled work',
    'skill.list': 'Available abilities',
    'skill.lifecycle': 'Ability history',
    'soul.get': 'Identity and guiding values',
    'swarm.list': 'Active helpers',
    'supervisor.list': 'Agent supervisor',
    'supervisor.get': 'Supervised outcome',
    'system.health': 'Service health',
    'system.metrics': 'Performance',
    'taskgraph.get': 'Goal map',
    'taskgraph.todo': 'Next steps',
    'tool.readiness': 'Actions ready to use',
    'tool.surface': 'Available actions',
    'work.brief': 'Current outcome and next action',
    'workflow.list': 'Reusable workflows',
	'workspace.capabilities': 'Workspace hosts',
  }
  return labels[operation as Operation] ?? operation.split('.').map(humanize).join(' · ')
}

function operationDescription(operation: Operation): string {
  const descriptions: Partial<Record<Operation, string>> = {
    'automatrix.list': 'Work Ion has proposed doing in the background.',
    'artifact.list': 'Deliverables recorded against completion criteria and their verification state.',
    'autonomy.get': 'Whether background initiative is off, suggest-only, paused, or limited to approved plans.',
    'channel.health': 'Whether messages can be delivered through each connection.',
    'channel.list': 'Places where you can talk with Ion.',
    'commands.catalog': 'Actions supported by this version of Ion.',
    'config.get': 'Non-secret settings that shape how Ion runs.',
    'continuity.brief': 'Completed work, failures, decisions, changed files, pending questions, discoveries, repairs, proposals, incomplete work, and deadlines from allowlisted durable evidence.',
    'curiosity.targets': 'Knowledge gaps Ion has noticed more than once.',
    'dreamweaver.beliefs': 'Carefully derived ideas supported by several saved sources.',
    'integrity.latest': 'The result of the most recent record-integrity check.',
    'logs.query': 'Recent operational activity, with private values removed.',
    'liveness.get': 'Current workload, timing, relationship, and communication signals.',
    'mcp.servers': 'External tool services available to Ion.',
    'mcp.tools': 'Actions supplied by connected tool services.',
    'memory.activation': 'Knowledge selected to help with the current conversation.',
    'memory.graph': 'How saved knowledge is connected and protected.',
    'memory.search': 'Facts, preferences, and other items explicitly saved for later.',
    'plugin.list': 'Optional add-ons installed for this Ion instance.',
	'project.list': 'Engineering projects attached to this account and their real workspace state.',
	'studio.intent.list': 'What Ion understood, visible assumptions, proposed behavior, risks, criteria, and approval state.',
    'policy.events': 'Recent allow, deny, and approval decisions.',
    'prediction.list': 'Expected results Ion will compare with what actually happens.',
    'premise.list': 'Unconfirmed ideas Ion is currently using while reasoning.',
    'provider.list': 'The AI model used for conversations, planning, and agent work.',
    'provider.usage': 'Recent model requests and failures.',
    'receipt.list': 'Recorded evidence for completed actions.',
    'receipt.verify': 'Whether recorded action evidence still matches.',
    'schedule.list': 'Recurring work and future checks.',
    'skill.list': 'Reusable procedures Ion can follow.',
    'skill.lifecycle': 'Active abilities, validation decisions, and retained versions available for rollback.',
    'soul.get': 'The stable values and behavior rules Ion follows.',
    'swarm.list': 'Additional agents currently helping with work.',
    'supervisor.list': 'Durable outcome plans, live per-specialist progress, budgets, retries, conflicts, and restart reconciliation.',
    'supervisor.get': 'One authoritative supervisor run with every specialist packet, attempt, lease, budget, and finding.',
    'system.health': 'Whether the main service is ready to accept work.',
    'system.metrics': 'Bounded service and event-stream performance information.',
    'taskgraph.get': 'The current plan and recent recovery state.',
    'taskgraph.todo': 'Plan steps that still need work.',
    'tool.readiness': 'Which actions can run now and which need setup.',
    'tool.surface': 'Actions currently available to the model.',
    'work.brief': 'The durable definition of success, next action, blockers, and verified completion coverage.',
    'workflow.list': 'Curated staged procedures with entry conditions, evidence, and human gates.',
	'workspace.capabilities': 'Local, container, and remote workspace capabilities negotiated with this runtime.',
  }
  return descriptions[operation] ?? 'Current information from the running Ion service.'
}

function humanize(value: string): string {
  const spaced = value.replaceAll('_', ' ')
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

function renderValue(value: unknown): string {
  if (Array.isArray(value)) return `${value.length} ${pluralize('item', value.length)}`
  if (isRecord(value)) {
    const count = Object.keys(value).length
    return `${count} ${pluralize('detail', count)}`
  }
  return formatScalar(value)
}

function compact(value: unknown): string {
  const encoded = JSON.stringify(value) ?? String(value)
  return encoded.length > 260 ? `${encoded.slice(0, 257)}…` : encoded
}

function stableKey(value: unknown, index: number): string {
  if (isRecord(value) && typeof value.id === 'string') return value.id
  return `${index}:${compact(value)}`
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
