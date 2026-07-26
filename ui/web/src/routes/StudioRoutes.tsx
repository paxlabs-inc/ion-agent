import { useQuery, useQueryClient } from '@tanstack/react-query'
import type {
  EventEnvelope,
  Operation,
  ProjectCIPatchPlan,
  ProjectDeliverySnapshot,
  ProjectIndex,
  ProjectRecord,
  ProjectSearchResponse,
  ProjectTerminalState,
  ProjectVerificationManifest,
  ProjectVerificationRun,
  ProjectVerificationWaiver,
  StudioIntent,
} from '@matrixmcl/ion-shared'
import {
  type FormEvent,
  type CSSProperties,
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useOperator, useOperatorState } from '../app/operator-context'
import { Icon } from '../components/Icon'
import { PanelResizeHandle } from '../components/PanelResizeHandle'
import { conciseProjectName } from '../lib/project-name'

interface ProjectListResult {
  revision: number
  projects: ProjectRecord[]
}

interface WorkBrief {
  contract?: {
    goal?: string
    deliverable?: string
    done_criteria?: Array<{ id?: string; description?: string }>
    verification_required?: string[]
  }
  completion_percentage?: number
  verified_criteria?: string[]
  unverified_criteria?: string[]
  next_action?: string
  blocking_reason?: string
}

interface GitProjection {
  version: string
  project_id: string
  workspace_revision: number
  repository_root: string
  head?: string
  branch?: string
  detached: boolean
  status: Array<{
    path: string
    original_path?: string
    index_status: string
    work_status: string
    untracked?: boolean
  }>
  branches: Array<{ name: string; commit: string; upstream?: string; current?: boolean }>
  remotes: Array<{ name: string; fetch_url?: string; push_url?: string }>
  history: Array<{ hash: string; author: string; authored_at: string; subject: string }>
  staged_diff?: string
  unstaged_diff?: string
  truncated: boolean
}

interface GitReview {
  workspace_revision: number
  head: string
  groups: Array<{
    criterion: string
    files: Array<{
      path: string
      original_path?: string
      subsystem: string
      kinds: string[]
      index_status: string
      work_status: string
      current_sha256: string
      diff?: string
      diff_truncated: boolean
    }>
  }>
}

interface GitReviewComment {
  id: string
  path: string
  line?: number
  criterion?: string
  body: string
  created_at: string
  resolved_at?: string
}

interface GitDiffSelection {
  project_id: string
  head: string
  patch: string
  sha256: string
  truncated: boolean
}

interface ProviderProjection {
  issues?: Array<{ id: string; number: number; title: string; state: string; web_url: string }>
  changes?: Array<{ id: string; number: number; title: string; state: string; draft: boolean; web_url: string }>
  review?: Array<{ id: string; path?: string; line?: number; body: string; resolved: boolean; outdated: boolean }>
  checks?: Array<{ id: string; name: string; status: string; conclusion?: string; web_url?: string }>
  mergeability?: { mergeable: boolean; state: string; reasons?: string[] }
}

interface PatchReceipt {
  patch_set_id: string
  workspace_revision: number
  status: string
  criteria: string[]
  validation_plan: string[]
  files: Array<{ path: string; before_sha256: string; after_sha256: string; generated?: boolean }>
  applied_at: string
  rollback_available: boolean
  classification: string
}

interface ToolchainReport {
  workspace_revision: number
  runtimes: Array<{ name: string; version?: string; available: boolean }>
  lockfiles: string[]
  package_manager?: string
  build_systems: string[]
  lifecycle_scripts: string[]
  required_versions: Record<string, string>
}

interface TerminalReplay {
  state: ProjectTerminalState
  from_cursor: number
  next_cursor: number
  output: string
  gap: boolean
}

interface TerminalControlLease {
  lease_id?: string
  owner: {
    turn_id?: string
    task_id?: string
    agent_id: string
    action: string
    revision: number
  }
  state: 'available' | 'active' | 'released' | 'expired'
  authority: 'executor' | 'operator'
  revision: number
  expires_at?: string
  reconciliation: string
}

interface RuntimePlan {
  version: string
  project_id: string
  workspace_revision: number
  stack: string
  working_directory: string
  commands: Array<{ kind: string; argv: string[]; description: string; network?: boolean }>
  default_service: string
  readiness_path: string
  inferred: boolean
  warnings?: string[]
}

interface RuntimeState {
  version: string
  id: string
  project_id: string
  workspace_revision: number
  name: string
  command_kind: string
  argv: string[]
  host: string
  port: number
  preview_url: string
  origin: string
  status: string
  next_action: string
  pid?: number
  reloads: number
  restarts: number
  logs?: string
  logs_truncated: boolean
  diagnostics?: Array<{ id: string; source: string; severity: string; code?: string; message: string; recurrence: number }>
  last_error?: string
  annotations?: Array<{ id: string; element_ref?: string; body: string; created_at: string }>
  style_proposals?: Array<{ id: string; element_ref: string; path: string; declarations: Record<string, string>; status: string }>
}

interface RuntimeProblem {
  id: string
  source: string
  severity: string
  code?: string
  message: string
  path?: string
  line?: number
  column?: number
  recurrence: number
  causal_evidence?: string[]
}

interface RuntimeInspection {
  url: string
  title: string
  text: string
  elements: Array<{ ref: string; tag: string; type?: string; text?: string; name?: string; placeholder?: string; disabled?: boolean }>
  accessibility?: Array<{ ref: string; rule: string; message: string }>
  screenshot_png: string
  screenshot_sha256: string
  width: number
  height: number
  dark_mode: boolean
  captured_at: string
}

type ProjectEntryMode = 'template' | 'clone' | 'attach' | 'import'
type StudioPanel =
  | 'plan'
  | 'changes'
  | 'code'
  | 'terminal'
  | 'preview'
  | 'problems'
  | 'tests'
  | 'security'
  | 'data'
  | 'deploy'

const panels: ReadonlyArray<{ id: StudioPanel; label: string }> = [
  { id: 'plan', label: 'Plan' },
  { id: 'changes', label: 'Changes' },
  { id: 'code', label: 'Code' },
  { id: 'terminal', label: 'Terminal' },
  { id: 'preview', label: 'Preview' },
  { id: 'problems', label: 'Problems' },
  { id: 'tests', label: 'Tests' },
  { id: 'security', label: 'Security' },
  { id: 'data', label: 'Data' },
  { id: 'deploy', label: 'Deploy' },
]

export function ProjectsHome() {
  const operator = useOperator()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [goal, setGoal] = useState('')
  const [mode, setMode] = useState<ProjectEntryMode>('template')
  const [name, setName] = useState('')
  const [template, setTemplate] = useState('static-web')
  const [repositoryURL, setRepositoryURL] = useState('')
  const [credentialReference, setCredentialReference] = useState('')
  const [directory, setDirectory] = useState('')
  const [archivePath, setArchivePath] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string>()
  const projects = useQuery({
    queryKey: ['studio', 'projects'],
    queryFn: async () => query<ProjectListResult>(operator, 'project.list', {}),
    retry: false,
  })

  const openProject = (project: ProjectRecord) => {
    navigate(`/studio/${project.id}`)
  }

  const continuePrompt = async (project: ProjectRecord, prompt: string) => {
    const content = prompt.trim()
    if (content === '') return
    let sessionID = operator.sessionID
    if (sessionID === undefined) {
      const created = await operator.command<{ id: string }>(
        'session.create',
        {},
        crypto.randomUUID(),
      )
      if (created.error !== undefined || created.result?.id === undefined) {
        throw new Error(created.error?.message ?? 'A project conversation could not be started.')
      }
      sessionID = created.result.id
      operator.setSessionID(sessionID)
    }
    const submitted = await operator.command(
      'turn.submit',
      {
        content,
        surface: 'studio',
        project_id: project.id,
      },
      crypto.randomUUID(),
      { session_id: sessionID },
    )
    if (submitted.error !== undefined) throw new Error(submitted.error.message)
  }

  const createProject = async (event: FormEvent) => {
    event.preventDefault()
    const projectName = name.trim() || conciseProjectName(goal)
    if (projectName === '') {
      setNotice('Name the project or describe what you want to build.')
      return
    }
    setBusy(true)
    setNotice(undefined)
    try {
      let operation: Operation
      let payload: Record<string, unknown>
      if (mode === 'clone') {
        operation = 'project.clone'
        payload = {
          name: projectName,
          repository_url: repositoryURL.trim(),
          credential_reference: credentialReference.trim(),
          authorized: true,
          host: 'direct_local',
          trust: 'untrusted',
        }
      } else if (mode === 'attach') {
        operation = 'project.attach'
        payload = { name: projectName, directory: directory.trim(), trust: 'untrusted' }
      } else if (mode === 'import') {
        operation = 'project.import'
        payload = {
          name: projectName,
          archive_path: archivePath.trim(),
          host: 'direct_local',
          trust: 'untrusted',
        }
      } else {
        operation = 'project.create'
        payload = {
          name: projectName,
          template,
          host: 'direct_local',
          trust: 'reviewed',
        }
      }
      const response = await operator.command<ProjectRecord>(
        operation,
        payload,
        crypto.randomUUID(),
      )
      if (response.error !== undefined || response.result === undefined) {
        throw new Error(response.error?.message ?? 'The project could not be created.')
      }
      await queryClient.invalidateQueries({ queryKey: ['studio', 'projects'] })
      await continuePrompt(response.result, goal)
      navigate(`/studio/${response.result.id}`)
    } catch (error) {
      setNotice(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }

  const found = projects.data?.projects ?? []
  return (
    <section className="studio-home" aria-labelledby="studio-home-title">
      <header className="studio-home-header">
        <p className="eyebrow">SOFTWARE STUDIO</p>
        <h1 id="studio-home-title">What do you want to build or change?</h1>
        <p>
          Describe the outcome in plain language. Ion keeps the request with the
          project, turns it into a reviewable plan, and uses the same conversation and safety controls.
        </p>
      </header>

      <form className="studio-start-card" onSubmit={createProject}>
        <label htmlFor="studio-goal">Your outcome</label>
        <textarea
          autoFocus
          id="studio-goal"
          onChange={(event) => setGoal(event.target.value)}
          placeholder="Build a calm project dashboard that works beautifully on phones…"
          rows={3}
          value={goal}
        />
        <div className="studio-start-row">
          <label>
            Project name
            <input
              onChange={(event) => setName(event.target.value)}
              placeholder="Generated from your request"
              value={name}
            />
          </label>
          <label>
            Start from
            <select onChange={(event) => setMode(event.target.value as ProjectEntryMode)} value={mode}>
              <option value="template">A template</option>
              <option value="clone">A Git repository</option>
              <option value="attach">A workspace directory</option>
              <option value="import">An archive</option>
            </select>
          </label>
        </div>
        {mode === 'template' ? (
          <label>
            Template
            <select onChange={(event) => setTemplate(event.target.value)} value={template}>
              <option value="static-web">Static web app</option>
              <option value="go-cli">Go command-line app</option>
              <option value="empty">Empty project</option>
            </select>
          </label>
        ) : null}
        {mode === 'clone' ? (
          <div className="studio-start-row">
            <label>
              Repository URL
              <input
                onChange={(event) => setRepositoryURL(event.target.value)}
                placeholder="https://github.com/organization/project.git"
                required
                type="url"
                value={repositoryURL}
              />
            </label>
            <label>
              Protected credential reference <span className="optional">optional</span>
              <input
                onChange={(event) => setCredentialReference(event.target.value)}
                placeholder="vault://github/read-project"
                value={credentialReference}
              />
            </label>
          </div>
        ) : null}
        {mode === 'attach' ? (
          <label>
            Directory available to the workspace host
            <input
              onChange={(event) => setDirectory(event.target.value)}
              placeholder="/srv/workspaces/my-project"
              required
              value={directory}
            />
          </label>
        ) : null}
        {mode === 'import' ? (
          <label>
            Archive available to the workspace host
            <input
              onChange={(event) => setArchivePath(event.target.value)}
              placeholder="/srv/imports/project.tar.gz"
              required
              value={archivePath}
            />
          </label>
        ) : null}
        <div className="studio-start-actions">
          <span>Private values stay in the vault; this form accepts references only.</span>
          <button disabled={busy} type="submit">
            <Icon name="spark" /> {busy ? 'Preparing project…' : 'Start building'}
          </button>
        </div>
        {notice === undefined ? null : <p className="studio-notice danger" role="alert">{notice}</p>}
      </form>

      <section className="recent-projects" aria-labelledby="recent-projects-title">
        <div className="studio-section-heading">
          <div>
            <span className="eyebrow">RECENT</span>
            <h2 id="recent-projects-title">Resume a project</h2>
          </div>
          <span>{found.length} {found.length === 1 ? 'project' : 'projects'}</span>
        </div>
        {projects.isPending ? <StudioSkeleton /> : projects.isError ? (
          <StudioEmpty title="Projects could not be checked" detail={errorMessage(projects.error)} />
        ) : found.length === 0 ? (
          <StudioEmpty
            title="No software projects yet"
            detail="Your first project will appear here with its exact resume state."
          />
        ) : (
          <div className="project-card-grid">
            {found.map((project) => (
              <button className="project-card" key={project.id} onClick={() => openProject(project)} type="button">
                <span className="project-card-icon"><Icon name="folder" /></span>
                <span className="project-card-copy">
                  <strong>{project.name}</strong>
                  <small>{humanize(project.source)} · {humanize(project.lifecycle)}</small>
                  <span>{project.stack_signals.length === 0 ? 'Stack discovery pending' : project.stack_signals.join(' · ')}</span>
                </span>
                <span className="project-card-meta">
                  <time dateTime={project.updated_at}>{relativeTime(project.updated_at)}</time>
                  <b>Resume</b>
                </span>
              </button>
            ))}
          </div>
        )}
      </section>
    </section>
  )
}

export function StudioWorkspace() {
  const { projectID = '' } = useParams()
  const operator = useOperator()
  const state = useOperatorState()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const requestedPanel = searchParams.get('panel') as StudioPanel | null
  const panel = panels.some((item) => item.id === requestedPanel) ? requestedPanel! : 'plan'
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([])
  const workspaceBodyRef = useRef<HTMLDivElement>(null)
  const [contextRailWidth, setContextRailWidth] = useState(280)
  const [contextResizing, setContextResizing] = useState(false)

  const project = useQuery({
    queryKey: ['studio', 'project', projectID],
    queryFn: async () => query<ProjectRecord>(operator, 'project.get', { project_id: projectID }),
    enabled: projectID !== '',
    retry: false,
    refetchInterval: Object.values(state.turns).some((turn) =>
      turn.status === 'running' &&
      (operator.sessionID === undefined || turn.session_id === operator.sessionID)
    ) ? 1_000 : 5_000,
  })
  const intents = useQuery({
    queryKey: ['studio', 'intents'],
    queryFn: async () => query<{ revision: number; intents: StudioIntent[] }>(operator, 'studio.intent.list', {}),
    retry: false,
    refetchInterval: 3_000,
  })
  const brief = useQuery({
    queryKey: ['studio', 'brief', operator.sessionID],
    queryFn: async () => query<WorkBrief>(operator, 'work.brief', {}, sessionScope(operator.sessionID)),
    retry: false,
    refetchInterval: 5_000,
  })
  const intent = (intents.data?.intents ?? []).find((item) => item.project_id === projectID)
  const activity = engineeringActivity(state.recent_events)

  useEffect(() => {
    if (project.data === undefined) return
    void queryClient.invalidateQueries({ queryKey: ['studio', projectID] })
  }, [project.data?.workspace_revision, projectID, queryClient])

  const setPanel = (next: StudioPanel) => {
    setSearchParams(next === 'plan' ? {} : { panel: next })
  }
  const tabKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight' && event.key !== 'Home' && event.key !== 'End') return
    event.preventDefault()
    const next = event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? panels.length - 1
        : (index + (event.key === 'ArrowRight' ? 1 : -1) + panels.length) % panels.length
    const target = panels[next]
    if (target !== undefined) {
      setPanel(target.id)
      tabRefs.current[next]?.focus()
    }
  }

  if (project.isPending) return <div className="studio-workspace-loading"><StudioSkeleton /></div>
  if (project.isError || project.data === undefined) {
    return (
      <section className="studio-workspace-error">
        <StudioEmpty title="This project is unavailable" detail={errorMessage(project.error)} />
        <button className="quiet-button" onClick={() => navigate('/studio')} type="button">Back to projects</button>
      </section>
    )
  }

  return (
    <section className="studio-workspace" aria-labelledby="studio-project-title">
      <header className="studio-project-header">
        <div className="studio-project-title">
          <Link aria-label="Back to Software Studio projects" to="/studio">Projects</Link>
          <span aria-hidden="true">/</span>
          <div>
            <h1 id="studio-project-title">{project.data.name}</h1>
            <span>{project.data.stack_signals.length === 0 ? 'Stack discovery pending' : project.data.stack_signals.join(' · ')}</span>
          </div>
        </div>
        <div className="studio-project-state">
          <span className={`studio-state-dot state-${project.data.lifecycle}`} aria-hidden="true" />
          <span>{humanize(project.data.lifecycle)}</span>
          <small>revision {project.data.workspace_revision}</small>
        </div>
      </header>

      <div
        className="studio-workspace-body"
        data-resizing={contextResizing}
        ref={workspaceBodyRef}
        style={{ '--studio-context-width': `${String(contextRailWidth)}px` } as CSSProperties}
      >
        <div className="studio-work-area">
          <nav aria-label="Project tools" className="studio-tabs" role="tablist">
            {panels.map((item, index) => (
              <button
                aria-controls={`studio-panel-${item.id}`}
                aria-selected={panel === item.id}
                id={`studio-tab-${item.id}`}
                key={item.id}
                onClick={() => setPanel(item.id)}
                onKeyDown={(event) => tabKeyDown(event, index)}
                ref={(node) => { tabRefs.current[index] = node }}
                role="tab"
                tabIndex={panel === item.id ? 0 : -1}
                type="button"
              >
                {item.label}
              </button>
            ))}
          </nav>
          <div
            aria-labelledby={`studio-tab-${panel}`}
            className="studio-panel"
            id={`studio-panel-${panel}`}
            role="tabpanel"
          >
            <StudioPanelContent
              brief={brief.data}
              intent={intent}
              panel={panel}
              project={project.data}
              setPanel={setPanel}
            />
          </div>
        </div>

        <PanelResizeHandle
          containerRef={workspaceBodyRef}
          defaultValue={280}
          direction="from-right"
          label="Resize project context"
          max={420}
          min={220}
          onChange={setContextRailWidth}
          onResizeStateChange={setContextResizing}
          oppositeMinimum={340}
          value={contextRailWidth}
        />

        <aside className="studio-context-rail" aria-label="Project context">
          <WorkBriefCard brief={brief.data} loading={brief.isPending} />
          <ActivityCard events={activity} />
          <details className="studio-project-details">
            <summary>Project details</summary>
            <dl>
              <div><dt>Workspace</dt><dd>{project.data.host === 'direct_local' ? 'Local' : humanize(project.data.host)}</dd></div>
              <div><dt>Trust</dt><dd>{humanize(project.data.trust)}</dd></div>
              <div><dt>Source</dt><dd>{humanize(project.data.source)}</dd></div>
              <div><dt>Managed</dt><dd>{project.data.managed ? 'Yes' : 'No'}</dd></div>
            </dl>
          </details>
        </aside>
      </div>
    </section>
  )
}

function StudioPanelContent({
  brief,
  intent,
  panel,
  project,
  setPanel,
}: {
  brief: WorkBrief | undefined
  intent: StudioIntent | undefined
  panel: StudioPanel
  project: ProjectRecord
  setPanel(panel: StudioPanel): void
}) {
  switch (panel) {
    case 'plan': return <PlanPanel brief={brief} intent={intent} project={project} />
    case 'changes': return <ChangesPanel project={project} />
    case 'code': return <CodePanel project={project} />
    case 'terminal': return <TerminalPanel project={project} />
    case 'preview': return <PreviewPanel project={project} />
    case 'problems': return <ProblemsPanel intent={intent} project={project} setPanel={setPanel} />
    case 'tests': return <TestsPanel brief={brief} intent={intent} project={project} setPanel={setPanel} />
    case 'security': return <SecurityPanel project={project} />
    case 'data': return <DataPanel project={project} />
    case 'deploy': return <DeployPanel project={project} />
  }
}

function PlanPanel({
  brief,
  intent,
  project,
}: {
  brief: WorkBrief | undefined
  intent: StudioIntent | undefined
  project: ProjectRecord
}) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [notice, setNotice] = useState<string>()
  const [decisions, setDecisions] = useState<Record<string, string>>({})
  const [planComment, setPlanComment] = useState('')
  const active = intent?.proposals.find((proposal) => proposal.id === intent.active_proposal_id)
    ?? intent?.proposals.at(-1)
  const tasks = active?.delta.tasks ?? []

  const decide = async (accept: boolean) => {
    if (intent === undefined || active === undefined) return
    const response = await operator.command(
      'studio.proposal.decide',
      {
        intent_id: intent.id,
        proposal_id: active.id,
        accept,
        reason: planComment.trim() || (
          accept
            ? 'Approved in the project workspace'
            : 'Returned for revision in the project workspace'
        ),
        assumption_decisions: decisions,
      },
      crypto.randomUUID(),
      sessionScope(operator.sessionID),
    )
    setNotice(response.error?.message ?? (accept ? 'Specification accepted.' : 'Specification returned for revision.'))
    if (response.error === undefined) {
      setPlanComment('')
      await queryClient.invalidateQueries({ queryKey: ['studio', 'intents'] })
    }
  }
  const apply = async () => {
    if (intent === undefined || active === undefined) return
    const response = await operator.command(
      'studio.proposal.apply',
      { intent_id: intent.id, proposal_id: active.id },
      crypto.randomUUID(),
      sessionScope(operator.sessionID),
    )
    setNotice(response.error?.message ?? 'The accepted specification is now authoritative.')
    if (response.error === undefined) await queryClient.invalidateQueries({ queryKey: ['studio', 'intents'] })
  }
  const revise = () => {
    window.dispatchEvent(new CustomEvent('ion:prefill-chat', {
      detail: `Revise the accepted plan for ${project.name}. I want to change: `,
    }))
  }

  if (intent === undefined) {
    return (
      <PanelSection title="Turn the outcome into a plan" kicker="PLAN">
        <StudioEmpty
          title="No project specification yet"
          detail="Describe the outcome in the conversation. Ion will surface assumptions, success criteria, risks, and a reviewable plan here before implementation."
        />
        <button onClick={() => window.dispatchEvent(new CustomEvent('ion:prefill-chat', {
          detail: `Create a reviewable specification for ${project.name}. The outcome is: `,
        }))} type="button">Describe the outcome</button>
      </PanelSection>
    )
  }

  return (
    <div className="studio-panel-stack">
      <PanelSection
        aside={<StatusPill tone={active?.status === 'accepted' ? 'success' : 'attention'}>{humanize(active?.status ?? 'draft')}</StatusPill>}
        kicker="OUTCOME"
        title={intent.goal}
      >
        <p className="studio-leading-copy">
          {brief?.contract?.deliverable ?? active?.rationale ?? 'The requested result is being clarified.'}
        </p>
        {intent.assumptions.length === 0 ? null : (
          <div className="studio-assumptions">
            <h3>Assumptions</h3>
            {intent.assumptions.map((assumption) => (
              <label className="studio-assumption" key={assumption.id}>
                <span>
                  <strong>{assumption.material ? 'Decision needed' : 'Reversible'}</strong>
                  {assumption.statement}
                </span>
                {assumption.material && assumption.resolution === undefined ? (
                  <input
                    aria-label={assumption.decision_needed ?? `Decision for ${assumption.statement}`}
                    onChange={(event) => setDecisions((current) => ({ ...current, [assumption.id]: event.target.value }))}
                    placeholder={assumption.decision_needed ?? 'Your decision'}
                    value={decisions[assumption.id] ?? ''}
                  />
                ) : null}
              </label>
            ))}
          </div>
        )}
        {active === undefined ? null : (
          <div className="studio-plan-actions">
            {active.status === 'proposed' ? (
              <label className="studio-plan-comment">
                Plan comment <span className="optional">saved with your decision</span>
                <textarea
                  onChange={(event) => setPlanComment(event.target.value)}
                  placeholder="What should Ion preserve or revise?"
                  rows={2}
                  value={planComment}
                />
              </label>
            ) : null}
            {active.status === 'proposed' ? (
              <>
                <button onClick={() => { void decide(true) }} type="button"><Icon name="check" /> Accept plan</button>
                <button className="quiet-button" onClick={() => { void decide(false) }} type="button">Request revision</button>
              </>
            ) : null}
            {active.status === 'accepted' && active.applied_at === undefined ? (
              <button onClick={() => { void apply() }} type="button">Apply to authoritative spec</button>
            ) : null}
            <button className="quiet-button" onClick={revise} type="button"><Icon name="edit" /> Revise in conversation</button>
          </div>
        )}
        {notice === undefined ? null : <p className="studio-notice" role="status">{notice}</p>}
      </PanelSection>

      <PanelSection kicker="IMPLEMENTATION" title="Work plan">
        {tasks.length === 0 ? (
          <StudioEmpty title="Tasks have not been generated" detail="Tasks appear after the specification contains a concrete implementation path." />
        ) : (
          <ol className="studio-task-list">
            {tasks.map((task, index) => (
              <li key={task.id}>
                <span className="studio-task-number">{index + 1}</span>
                <div>
                  <strong>{task.title}</strong>
                  <span>{task.criteria.length} success {task.criteria.length === 1 ? 'criterion' : 'criteria'}</span>
                </div>
                <StatusPill tone="quiet">Planned</StatusPill>
              </li>
            ))}
          </ol>
        )}
      </PanelSection>

      {active === undefined ? null : (
        <div className="studio-two-column">
          <PanelSection kicker="VERIFY" title="How success is checked">
            <PlainList values={active.delta.acceptance_criteria.map((criterion) => criterion.description)} empty="No acceptance criteria yet." />
          </PanelSection>
          <PanelSection kicker="BOUNDARIES" title="Risks and rollback">
            <PlainList values={[...active.delta.risks, ...active.delta.rollback]} empty="No special risks are recorded." />
          </PanelSection>
        </div>
      )}
    </div>
  )
}

function ChangesPanel({ project }: { project: ProjectRecord }) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [commentPath, setCommentPath] = useState('')
  const [commentBody, setCommentBody] = useState('')
  const [notice, setNotice] = useState<string>()
  const [selectedPaths, setSelectedPaths] = useState<string[]>([])
  const [hunkDiff, setHunkDiff] = useState<GitDiffSelection>()
  const [hunkPatch, setHunkPatch] = useState('')
  const [branchName, setBranchName] = useState('')
  const [commitMessage, setCommitMessage] = useState('')
  const [tagName, setTagName] = useState('')
  const [remoteName, setRemoteName] = useState('origin')
  const [remoteBranch, setRemoteBranch] = useState('main')
  const [providerName, setProviderName] = useState('github')
  const [repositoryName, setRepositoryName] = useState('')
  const [permissionGrant, setPermissionGrant] = useState('')
  const [remoteHead, setRemoteHead] = useState('')
  const [mergeRevision, setMergeRevision] = useState('')
  const [providerProjection, setProviderProjection] = useState<ProviderProjection>()
  const [draftTitle, setDraftTitle] = useState('')
  const git = useQuery({
    queryKey: ['studio', project.id, 'git'],
    queryFn: async () => query<GitProjection>(operator, 'project.git.get', { project_id: project.id }),
    retry: false,
  })
  const review = useQuery({
    queryKey: ['studio', project.id, 'review'],
    queryFn: async () => query<GitReview>(operator, 'project.git.review.get', { project_id: project.id }),
    retry: false,
  })
  const comments = useQuery({
    queryKey: ['studio', project.id, 'comments'],
    queryFn: async () => query<GitReviewComment[]>(operator, 'project.git.review.comments', { project_id: project.id }),
    retry: false,
  })
  const history = useQuery({
    queryKey: ['studio', project.id, 'patches'],
    queryFn: async () => query<PatchReceipt[]>(operator, 'project.patch.history', { project_id: project.id }),
    retry: false,
  })
  const reviewGroups = Array.isArray(review.data?.groups) ? review.data.groups : []
  const files = reviewGroups.flatMap((group) => Array.isArray(group.files) ? group.files : [])
  const reviewComments = Array.isArray(comments.data) ? comments.data : []

  useEffect(() => {
    if (commentPath === '' && files[0] !== undefined) setCommentPath(files[0].path)
  }, [commentPath, files])
  useEffect(() => {
    setSelectedPaths((current) => current.filter((path) => files.some((file) => file.path === path)))
  }, [files])

  const refreshGit = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'git'] }),
      queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'review'] }),
      queryClient.invalidateQueries({ queryKey: ['studio', 'project', project.id] }),
    ])
  }

  const addComment = async (event: FormEvent) => {
    event.preventDefault()
    const response = await operator.command(
      'project.git.review.comment',
      { project_id: project.id, path: commentPath, body: commentBody },
      crypto.randomUUID(),
    )
    setNotice(response.error?.message ?? 'Review comment saved as a durable work item.')
    if (response.error === undefined) {
      setCommentBody('')
      await queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'comments'] })
    }
  }
  const resolveComment = async (commentID: string) => {
    const response = await operator.command(
      'project.git.review.resolve',
      { project_id: project.id, comment_id: commentID },
      crypto.randomUUID(),
    )
    setNotice(response.error?.message ?? 'Review comment resolved.')
    if (response.error === undefined) await queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'comments'] })
  }
  const rollbackPatch = async (receipt: PatchReceipt) => {
    const response = await operator.command(
      'project.patch.rollback',
      {
        project_id: project.id,
        patch_set_id: receipt.patch_set_id,
        workspace_revision: project.workspace_revision,
      },
      crypto.randomUUID(),
    )
    setNotice(response.error?.message ?? 'The selected project version was restored transactionally.')
    if (response.error === undefined) {
      await Promise.all([
        refreshGit(),
        queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'patches'] }),
      ])
    }
  }
  const stageFile = async (file: GitReview['groups'][number]['files'][number]) => {
    if (git.data?.head === undefined) return
    const response = await operator.command(
      'project.git.stage',
      {
        project_id: project.id,
        workspace_revision: project.workspace_revision,
        expected_head: git.data.head,
        paths: [{ path: file.path, sha256: file.current_sha256 }],
      },
      crypto.randomUUID(),
    )
    setNotice(response.error?.message ?? `${file.path} staged from the exact reviewed content.`)
    if (response.error === undefined) {
      await refreshGit()
    }
  }
  const prepareHunks = async (file: GitReview['groups'][number]['files'][number]) => {
    try {
      const result = await query<GitDiffSelection>(operator, 'project.git.diff', { project_id: project.id, paths: [file.path] })
      setHunkDiff(result)
      setHunkPatch(result.patch)
      setSelectedPaths([file.path])
      setNotice(result.truncated ? 'The diff is truncated and cannot be safely staged by hunk.' : 'Edit the patch below to keep only reviewed hunks.')
    } catch (error) {
      setNotice(errorMessage(error))
    }
  }
  const stageHunks = async () => {
    if (git.data?.head === undefined || hunkDiff === undefined) return
    const expectations = files.filter((file) => selectedPaths.includes(file.path)).map((file) => ({ path: file.path, sha256: file.current_sha256 }))
    const response = await operator.command('project.git.stage.hunks', {
      project_id: project.id, workspace_revision: project.workspace_revision, expected_head: git.data.head,
      diff_sha256: hunkDiff.sha256, patch: hunkPatch, paths: expectations,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Only the reviewed hunks were staged against the exact current diff.')
    if (response.error === undefined) { setHunkDiff(undefined); setHunkPatch(''); await refreshGit() }
  }
  const createBranch = async (event: FormEvent) => {
    event.preventDefault()
    const response = await operator.command('project.git.branch.create', { project_id: project.id,
      workspace_revision: project.workspace_revision, expected_head: git.data?.head, name: branchName }, crypto.randomUUID())
    setNotice(response.error?.message ?? `Branch ${branchName} created without switching the active tree.`)
    if (response.error === undefined) { setBranchName(''); await refreshGit() }
  }
  const commit = async (checkpoint: boolean) => {
    if (git.data?.head === undefined) return
    const paths = files.filter((file) => selectedPaths.includes(file.path)).map((file) => ({ path: file.path, sha256: file.current_sha256 }))
    const response = await operator.command(checkpoint ? 'project.git.checkpoint' : 'project.git.commit', {
      project_id: project.id, workspace_revision: project.workspace_revision, expected_head: git.data.head,
      message: commitMessage, author_name: 'Ion Operator', author_email: 'ion@localhost', paths,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? `${checkpoint ? 'Checkpoint' : 'Commit'} created from ${String(paths.length)} exact reviewed paths.`)
    if (response.error === undefined) { setCommitMessage(''); setSelectedPaths([]); await refreshGit() }
  }
  const createTag = async (event: FormEvent) => {
    event.preventDefault()
    const response = await operator.command('project.git.tag.create', { project_id: project.id,
      workspace_revision: project.workspace_revision, expected_head: git.data?.head, name: tagName,
      message: `Release ${tagName}`, author_name: 'Ion Operator', author_email: 'ion@localhost' }, crypto.randomUUID())
    setNotice(response.error?.message ?? `Annotated tag ${tagName} created.`)
    if (response.error === undefined) { setTagName(''); await refreshGit() }
  }
  const issueGrant = async (actions: string[]) => {
    const response = await operator.command<{ permission_grant: string }>('project.git.provider.grant', {
      project_id: project.id, provider: providerName, repository: repositoryName, actions, ttl_seconds: 600,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? `A 10-minute ${actions.join(', ')} grant is ready for this exact repository.`)
    if (response.result?.permission_grant !== undefined) setPermissionGrant(response.result.permission_grant)
  }
  const remoteOperation = async (operation: 'project.git.sync' | 'project.git.pull' | 'project.git.push' | 'project.git.force-with-lease') => {
    if (git.data?.head === undefined) return
    const payload: Record<string, unknown> = { project_id: project.id, workspace_revision: project.workspace_revision,
      expected_head: git.data.head, provider: providerName, remote: remoteName, permission_grant: permissionGrant }
    if (operation === 'project.git.pull') payload.branch = remoteBranch
    if (operation === 'project.git.push' || operation === 'project.git.force-with-lease') Object.assign(payload, {
      source_revision: git.data.head, target_branch: remoteBranch, expected_remote_head: remoteHead,
      idempotency_key: crypto.randomUUID(),
    })
    const response = await operator.command(operation, payload, crypto.randomUUID())
    setNotice(response.error?.message ?? `${humanize(operation.split('.').at(-1) ?? operation)} completed with a durable receipt.`)
    if (response.error === undefined) await refreshGit()
  }
  const merge = async () => {
    const response = await operator.command('project.git.merge', { project_id: project.id,
      workspace_revision: project.workspace_revision, expected_head: git.data?.head, revision: mergeRevision }, crypto.randomUUID())
    setNotice(response.error?.message ?? `Merged ${mergeRevision} after verifying a clean working tree.`)
    if (response.error === undefined) { setMergeRevision(''); await refreshGit() }
  }
  const loadProvider = async () => {
    try {
      const base = { project_id: project.id, provider: providerName, repository: repositoryName, permission_grant: permissionGrant }
      const [issues, changes] = await Promise.all([
        query<ProviderProjection>(operator, 'project.git.provider.issues', base),
        query<ProviderProjection>(operator, 'project.git.provider.changes', base),
      ])
      setProviderProjection({ issues: issues.issues ?? [], changes: changes.changes ?? [] })
      setNotice('Provider issues and change requests are normalized into the Studio review model.')
    } catch (error) {
      setNotice(errorMessage(error))
    }
  }
  const createDraft = async () => {
    if (git.data?.head === undefined) return
    const response = await operator.command('project.git.provider.draft', { project_id: project.id, provider: providerName,
      repository: repositoryName, source_branch: git.data.branch, target_branch: remoteBranch, title: draftTitle,
      body: 'Created from reviewed Ion Software Studio changes.', expected_head: git.data.head,
      idempotency_key: crypto.randomUUID(), permission_grant: permissionGrant }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'A draft change request was created exactly once and reconciled by marker.')
    if (response.error === undefined) setDraftTitle('')
  }

  return (
    <div className="studio-panel-stack">
      <PanelSection
        aside={git.data === undefined ? undefined : <StatusPill tone={(git.data.status ?? []).length === 0 ? 'success' : 'attention'}>{(git.data.status ?? []).length} changed</StatusPill>}
        kicker="SOURCE CONTROL"
        title="Changes"
      >
        {git.isPending ? <StudioSkeleton /> : git.isError || git.data === undefined ? (
          <StudioEmpty
            title="Git history is not available for this project"
            detail="The workspace is still safe to edit. Clone a repository or initialize Git through the supervised terminal to enable branch, diff, review, and history controls."
          />
        ) : (
          <>
            <div className="git-summary">
              <div><span>Branch</span><strong>{git.data.detached ? 'Detached HEAD' : (git.data.branch ?? 'No branch')}</strong></div>
              <div><span>Commit</span><strong><code>{shortHash(git.data.head)}</code></strong></div>
              <div><span>Remote</span><strong>{git.data.remotes[0]?.name ?? 'Not configured'}</strong></div>
            </div>
            {files.length === 0 ? (
              <StudioEmpty title="Working tree is clean" detail="No staged, unstaged, or untracked files need review." />
            ) : (
              <div className="change-list">
                {reviewGroups.map((group) => (
                  <section key={group.criterion}>
                    <header><span>Criterion</span><strong>{humanize(group.criterion)}</strong></header>
                    {group.files.map((file) => (
                      <details className="change-file" key={file.path}>
                        <summary>
                          <span className="change-status">{file.index_status !== ' ' ? file.index_status : file.work_status}</span>
                          <strong>{file.path}</strong>
                          <span className="change-kinds">{file.kinds.join(' · ')}</span>
                        </summary>
                        <div>
                          {file.diff === undefined ? <p>No textual diff is available for this file.</p> : <pre><code>{file.diff}</code></pre>}
                          <div className="change-file-actions">
                            <label><input checked={selectedPaths.includes(file.path)} onChange={(event) => setSelectedPaths((current) => event.target.checked ? [...new Set([...current, file.path])] : current.filter((path) => path !== file.path))} type="checkbox" /> Include in next exact commit</label>
                            <button className="quiet-button" disabled={file.current_sha256 === '' || git.data?.head === undefined} onClick={() => { void stageFile(file) }} type="button">Stage exact reviewed file</button>
                            <button className="quiet-button" disabled={file.diff === undefined || file.diff_truncated} onClick={() => { void prepareHunks(file) }} type="button">Select hunks</button>
                          </div>
                        </div>
                      </details>
                    ))}
                  </section>
                ))}
              </div>
            )}
          </>
        )}
      </PanelSection>

      {hunkDiff === undefined ? null : (
        <PanelSection kicker="PARTIAL STAGING" title="Review selected hunks">
          <p className="studio-leading-copy">Delete any hunks you do not want staged. Path headers remain confined to the reviewed file and the backend rechecks the complete diff hash before applying.</p>
          <textarea className="hunk-editor" onChange={(event) => setHunkPatch(event.target.value)} rows={18} spellCheck={false} value={hunkPatch} />
          <div className="studio-plan-actions"><button disabled={hunkDiff.truncated || hunkPatch.trim() === ''} onClick={() => { void stageHunks() }} type="button">Stage reviewed hunks</button><button className="quiet-button" onClick={() => setHunkDiff(undefined)} type="button">Cancel</button></div>
        </PanelSection>
      )}

      {git.data === undefined ? null : (
        <div className="studio-two-column">
          <PanelSection kicker="LOCAL HISTORY" title="Branch, commit, and tag">
            <form className="compact-control-form" onSubmit={createBranch}><label>New branch<input onChange={(event) => setBranchName(event.target.value)} placeholder="feature/reviewed-change" required value={branchName} /></label><button type="submit">Create branch</button></form>
            <div className="compact-control-form"><label>Commit message<input onChange={(event) => setCommitMessage(event.target.value)} placeholder="Describe the reviewed change" value={commitMessage} /></label><div className="studio-plan-actions"><button disabled={commitMessage.trim() === '' || selectedPaths.length === 0} onClick={() => { void commit(false) }} type="button">Commit {selectedPaths.length} selected</button><button className="quiet-button" disabled={commitMessage.trim() === '' || selectedPaths.length === 0} onClick={() => { void commit(true) }} type="button">Create checkpoint</button></div></div>
            <form className="compact-control-form" onSubmit={createTag}><label>Annotated tag<input onChange={(event) => setTagName(event.target.value)} placeholder="v1.0.0" required value={tagName} /></label><button type="submit">Create tag</button></form>
            <div className="compact-control-form"><label>Merge revision<input onChange={(event) => setMergeRevision(event.target.value)} placeholder="origin/main or commit" value={mergeRevision} /></label><button disabled={mergeRevision.trim() === ''} onClick={() => { void merge() }} type="button">Merge reviewed revision</button></div>
          </PanelSection>
          <PanelSection kicker="REMOTE EFFECTS" title="Sync and publish">
            <div className="remote-control-grid"><label>Provider<select onChange={(event) => setProviderName(event.target.value)} value={providerName}><option value="github">GitHub</option><option value="gitlab">GitLab</option><option value="local">Local test remote</option></select></label><label>Repository scope<input onChange={(event) => setRepositoryName(event.target.value)} placeholder="organization/repository" value={repositoryName} /></label><label>Remote<input onChange={(event) => setRemoteName(event.target.value)} value={remoteName} /></label><label>Branch<input onChange={(event) => setRemoteBranch(event.target.value)} value={remoteBranch} /></label><label>Expected remote head<input onChange={(event) => setRemoteHead(event.target.value)} placeholder="Required for force-with-lease" value={remoteHead} /></label></div>
            <p className="studio-muted">Remote actions require a short-lived grant bound to this actor, project, provider, repository, and exact action. Force push is only available as force-with-lease.</p>
            <div className="studio-plan-actions"><button className="quiet-button" disabled={repositoryName.trim() === ''} onClick={() => { void issueGrant(['read']) }} type="button">Authorize read</button><button className="quiet-button" disabled={repositoryName.trim() === ''} onClick={() => { void issueGrant(['push']) }} type="button">Authorize push</button><button className="quiet-button danger-button" disabled={repositoryName.trim() === ''} onClick={() => { void issueGrant(['force-push']) }} type="button">Authorize force lease</button></div>
            <div className="studio-plan-actions"><button disabled={permissionGrant === ''} onClick={() => { void remoteOperation('project.git.sync') }} type="button">Sync</button><button disabled={permissionGrant === ''} onClick={() => { void remoteOperation('project.git.pull') }} type="button">Pull</button><button disabled={permissionGrant === ''} onClick={() => { void remoteOperation('project.git.push') }} type="button">Push</button><button className="danger-button" disabled={permissionGrant === '' || remoteHead.trim() === ''} onClick={() => { void remoteOperation('project.git.force-with-lease') }} type="button">Force with lease</button></div>
          </PanelSection>
        </div>
      )}

      {git.data === undefined ? null : (
        <PanelSection kicker="PROVIDER REVIEW" title="Issues and change requests">
          <div className="studio-plan-actions"><button disabled={permissionGrant === '' || repositoryName.trim() === ''} onClick={() => { void loadProvider() }} type="button">Load issues and changes</button><button className="quiet-button" disabled={repositoryName.trim() === ''} onClick={() => { void issueGrant(['draft.create']) }} type="button">Authorize draft request</button></div>
          {providerProjection === undefined ? <p className="studio-muted">Choose a provider and exact repository above, then issue a short-lived read grant.</p> : <div className="provider-review-grid"><section><strong>Issues</strong><PlainList values={(providerProjection.issues ?? []).map((item) => `#${String(item.number)} ${item.title} · ${item.state}`)} empty="No issues returned." /></section><section><strong>Change requests</strong><PlainList values={(providerProjection.changes ?? []).map((item) => `#${String(item.number)} ${item.title} · ${item.draft ? 'draft' : item.state}`)} empty="No change requests returned." /></section></div>}
          <div className="compact-control-form"><label>Draft title<input onChange={(event) => setDraftTitle(event.target.value)} placeholder="Summarize the reviewed change" value={draftTitle} /></label><button disabled={permissionGrant === '' || draftTitle.trim() === '' || git.data.detached} onClick={() => { void createDraft() }} type="button">Create draft change request</button></div>
        </PanelSection>
      )}

      <div className="studio-two-column">
        <PanelSection kicker="REVIEW" title="Comments">
          {reviewComments.length === 0 ? <p className="studio-muted">No review comments.</p> : (
            <ul className="review-comment-list">
              {reviewComments.map((comment) => (
                <li key={comment.id} data-resolved={comment.resolved_at !== undefined}>
                  <strong>{comment.path}</strong>
                  <p>{comment.body}</p>
                  {comment.resolved_at === undefined ? (
                    <button className="quiet-button" onClick={() => { void resolveComment(comment.id) }} type="button">Resolve</button>
                  ) : <span>Resolved</span>}
                </li>
              ))}
            </ul>
          )}
          {files.length === 0 ? null : (
            <form className="review-comment-form" onSubmit={addComment}>
              <label>File<select onChange={(event) => setCommentPath(event.target.value)} value={commentPath}>{files.map((file) => <option key={file.path}>{file.path}</option>)}</select></label>
              <label>Comment<textarea onChange={(event) => setCommentBody(event.target.value)} required rows={2} value={commentBody} /></label>
              <button type="submit">Add review comment</button>
            </form>
          )}
        </PanelSection>
        <PanelSection kicker="TRANSACTIONS" title="Applied patches">
          {history.isPending ? <StudioSkeleton /> : (history.data ?? []).length === 0 ? (
            <StudioEmpty title="No transactional edits yet" detail="Verified patch receipts and rollback availability appear here after Ion edits project files." />
          ) : (
            <ol className="patch-history">
              {(history.data ?? []).slice().reverse().map((receipt) => (
                <li key={receipt.patch_set_id}>
                  <span className="studio-task-number"><Icon name="check" /></span>
                  <div><strong>{receipt.files.length} {receipt.files.length === 1 ? 'file' : 'files'}</strong><span>{receipt.criteria.join(' · ') || 'Transactional edit'} · revision {receipt.workspace_revision}</span></div>
                  <StatusPill tone={receipt.rollback_available ? 'success' : 'quiet'}>{receipt.rollback_available ? 'Rollback ready' : humanize(receipt.status)}</StatusPill>
                  {receipt.rollback_available ? (
                    <button className="quiet-button" onClick={() => { void rollbackPatch(receipt) }} type="button">
                      Restore this version
                    </button>
                  ) : null}
                </li>
              ))}
            </ol>
          )}
        </PanelSection>
      </div>
      {notice === undefined ? null : <p className="studio-notice" role="status">{notice}</p>}
    </div>
  )
}

function CodePanel({ project }: { project: ProjectRecord }) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [searchKind, setSearchKind] = useState('lexical')
  const [matches, setMatches] = useState<ProjectSearchResponse>()
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string>()
  const index = useQuery({
    queryKey: ['studio', project.id, 'index'],
    queryFn: async () => query<ProjectIndex>(operator, 'project.index.get', { project_id: project.id }),
    retry: false,
  })
  const refresh = async () => {
    setBusy(true)
    const response = await operator.command<ProjectIndex>(
      'project.index.refresh',
      { project_id: project.id, workspace_revision: project.workspace_revision },
      crypto.randomUUID(),
    )
    setBusy(false)
    setNotice(response.error?.message ?? 'The project index is current.')
    if (response.error === undefined) await queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'index'] })
  }
  const runSearch = async (event: FormEvent) => {
    event.preventDefault()
    if (index.data === undefined) return
    setBusy(true)
    try {
      setMatches(await query<ProjectSearchResponse>(operator, 'project.search', {
        project_id: project.id,
        workspace_revision: project.workspace_revision,
        expected_index_revision: index.data.index_revision,
        kind: searchKind,
        query: search,
        limit: 30,
        max_result_bytes: 128_000,
      }))
      setNotice(undefined)
    } catch (error) {
      setNotice(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  const files = index.data?.files.filter((file) => file.class === 'source' || file.class === 'generated') ?? []
  return (
    <div className="studio-panel-stack">
      <PanelSection
        aside={<button className="quiet-button" disabled={busy} onClick={() => { void refresh() }} type="button">{busy ? 'Refreshing…' : 'Refresh index'}</button>}
        kicker="PROJECT INDEX"
        title="Code"
      >
        {index.isPending ? <StudioSkeleton /> : index.isError || index.data === undefined ? (
          <StudioEmpty title="The code index has not been built" detail="Refresh the index to inspect root-confined project files, symbols, configuration, and diagnostics." />
        ) : (
          <>
            <form className="code-search" onSubmit={runSearch}>
              <label className="sr-only" htmlFor="studio-code-search">Search project code</label>
              <Icon name="search" />
              <input id="studio-code-search" onChange={(event) => setSearch(event.target.value)} placeholder="Search files and symbols…" required value={search} />
              <select aria-label="Search type" onChange={(event) => setSearchKind(event.target.value)} value={searchKind}>
                <option value="lexical">Text</option>
                <option value="filename">File name</option>
                <option value="symbol">Symbol</option>
                <option value="reference">Reference</option>
                <option value="diagnostic">Diagnostic</option>
                <option value="semantic">Meaning</option>
              </select>
              <button disabled={busy} type="submit">Search</button>
            </form>
            <div className="code-layout">
              <section className="file-browser" aria-label="Project files">
                <header><strong>Files</strong><span>{files.length}</span></header>
                {files.length === 0 ? <p>No indexable files found.</p> : (
                  <ul>{files.slice(0, 250).map((file) => <li key={file.path}><Icon name="archive" /><span>{file.path}</span><small>{file.language ?? humanize(file.class)}</small></li>)}</ul>
                )}
              </section>
              <section className="search-results" aria-live="polite">
                <header><strong>{matches === undefined ? 'Project map' : 'Search results'}</strong><span>{matches?.matches.length ?? index.data.languages.length} found</span></header>
                {matches === undefined ? (
                  <div className="project-map">
                    <div><span>Languages</span><strong>{index.data.languages.join(', ') || 'Not detected'}</strong></div>
                    <div><span>Frameworks</span><strong>{index.data.frameworks.join(', ') || 'Not detected'}</strong></div>
                    <div><span>Entry points</span><strong>{index.data.entry_points.length}</strong></div>
                    <div><span>Index revision</span><strong>{index.data.index_revision}</strong></div>
                  </div>
                ) : matches.matches.length === 0 ? (
                  <StudioEmpty title="No matches" detail="Try a broader term or a different search type." />
                ) : (
                  <ol>
                    {matches.matches.map((match, index) => (
                      <li key={`${match.path}-${String(match.line_start)}-${String(index)}`}>
                        <header><strong>{match.path}</strong><span>{match.line_start === undefined ? humanize(match.kind) : `line ${match.line_start}`}</span></header>
                        <pre><code>{match.snippet}</code></pre>
                      </li>
                    ))}
                  </ol>
                )}
              </section>
            </div>
          </>
        )}
        {notice === undefined ? null : <p className="studio-notice" role="status">{notice}</p>}
      </PanelSection>
    </div>
  )
}

function TerminalPanel({ project }: { project: ProjectRecord }) {
  const operator = useOperator()
  const [executable, setExecutable] = useState('')
  const [argumentsText, setArgumentsText] = useState('')
  const [workingDirectory, setWorkingDirectory] = useState('')
  const [mode, setMode] = useState<'one_shot' | 'pty'>('one_shot')
  const [terminal, setTerminal] = useState<ProjectTerminalState>()
  const [terminalInput, setTerminalInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string>()
  const replay = useQuery({
    queryKey: ['studio', project.id, 'terminal', terminal?.id],
    queryFn: async () => query<TerminalReplay>(operator, 'project.terminal.replay', { terminal_id: terminal?.id, cursor: 0 }),
    enabled: terminal?.id !== undefined,
    retry: false,
    refetchInterval: (result) => {
      const status = result.state.data?.state.status
      return status === 'running' || status === 'starting' ? 650 : false
    },
  })
  const control = useQuery({
    queryKey: ['studio', project.id, 'terminal-control', terminal?.id],
    queryFn: async () => query<TerminalControlLease>(
      operator,
      'computer.control.get',
      { resource_kind: 'terminal', resource_id: terminal?.id },
      sessionScope(operator.sessionID),
    ),
    enabled: terminal?.id !== undefined,
    retry: false,
    refetchInterval: terminal?.id === undefined ? false : 1_000,
  })
  const start = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    const argv = [executable.trim(), ...argumentsText.split('\n').map((item) => item.trim()).filter(Boolean)]
    const response = await operator.command<ProjectTerminalState>(
      'project.process.start',
      {
        project_id: project.id,
        workspace_revision: project.workspace_revision,
        mode,
        argv,
        working_directory: workingDirectory.trim(),
        timeout_seconds: 900,
        output_bytes: 1_048_576,
        columns: 120,
        rows: 34,
      },
      crypto.randomUUID(),
      sessionScope(operator.sessionID),
    )
    setBusy(false)
    if (response.error !== undefined || response.result === undefined) {
      setNotice(response.error?.message ?? 'The supervised process could not start.')
      return
    }
    setTerminal(response.result)
    setNotice('Process started in the confined project workspace.')
  }
  const terminalCommand = async (operation: 'project.terminal.input' | 'project.terminal.signal' | 'project.terminal.cancel', payload: Record<string, unknown>) => {
    if (terminal === undefined) return
    const lease = control.data
    if (lease?.lease_id === undefined || lease.state !== 'active') {
      setNotice('Take terminal control before sending input or signals.')
      return
    }
    const response = await operator.command(operation, {
      terminal_id: terminal.id,
      lease_id: lease.lease_id,
      lease_revision: lease.revision,
      ...payload,
    }, crypto.randomUUID(), sessionScope(operator.sessionID))
    setNotice(response.error?.message ?? 'Terminal control accepted.')
    if (operation === 'project.terminal.input' && response.error === undefined) setTerminalInput('')
    await replay.refetch()
  }
  const changeControl = async (
    operation: 'computer.control.acquire' | 'computer.control.renew' | 'computer.control.release',
  ) => {
    if (terminal === undefined || control.data === undefined) return
    const current = control.data
    const payload = operation === 'computer.control.acquire'
      ? {
          resource_kind: 'terminal',
          resource_id: terminal.id,
          target_revision: current.owner.revision,
          owner: current.owner,
          expected_lease_revision: current.revision,
          ttl_seconds: 90,
        }
      : {
          resource_kind: 'terminal',
          resource_id: terminal.id,
          lease_id: current.lease_id,
          expected_lease_revision: current.revision,
          ...(operation === 'computer.control.renew' ? { ttl_seconds: 90 } : {}),
        }
    const response = await operator.command<TerminalControlLease>(
      operation,
      payload,
      crypto.randomUUID(),
      sessionScope(operator.sessionID),
    )
    setNotice(response.error?.message ?? (
      operation === 'computer.control.acquire'
        ? 'Terminal control acquired at the executor boundary.'
        : operation === 'computer.control.renew'
          ? 'Terminal control renewed.'
          : 'Terminal control returned to the executor.'
    ))
    await control.refetch()
  }
  const current = replay.data?.state ?? terminal
  return (
    <div className="studio-panel-stack">
      <PanelSection
        aside={current === undefined ? undefined : <StatusPill tone={current.status === 'running' ? 'success' : 'quiet'}>{humanize(current.status)}</StatusPill>}
        kicker="SUPERVISED PROCESS"
        title="Terminal"
      >
        <form className="terminal-launcher" onSubmit={start}>
          <label>Executable<input autoComplete="off" onChange={(event) => setExecutable(event.target.value)} placeholder="npm" required value={executable} /></label>
          <label>Arguments <span className="optional">one per line</span><textarea autoComplete="off" onChange={(event) => setArgumentsText(event.target.value)} placeholder={'run\ntest'} rows={3} value={argumentsText} /></label>
          <div className="studio-start-row">
            <label>Working directory <span className="optional">relative to project</span><input onChange={(event) => setWorkingDirectory(event.target.value)} placeholder="ui/web" value={workingDirectory} /></label>
            <label>Mode<select onChange={(event) => setMode(event.target.value as 'one_shot' | 'pty')} value={mode}><option value="one_shot">Run once</option><option value="pty">Interactive terminal</option></select></label>
          </div>
          <div className="terminal-launch-actions">
            <span>Commands are sent as an argument vector; no shell interpolation is used.</span>
            <button disabled={busy} type="submit">{busy ? 'Starting…' : 'Run command'}</button>
          </div>
        </form>

        <section className="terminal-window" aria-label="Terminal output">
          <header>
            <span className="terminal-lights" aria-hidden="true"><i /><i /><i /></span>
            <strong>{current === undefined ? 'No active terminal' : current.argv.join(' ')}</strong>
            <span>{current === undefined ? 'ready' : humanize(current.status)}</span>
          </header>
          <pre aria-live="polite"><code>{replay.data?.output ?? 'Run a command to see its bounded, redacted output here.'}</code></pre>
          {current?.mode === 'pty' && current.status === 'running' ? (
            <form className="terminal-input" onSubmit={(event) => { event.preventDefault(); void terminalCommand('project.terminal.input', { input: `${terminalInput}\n` }) }}>
              <label className="sr-only" htmlFor="terminal-input">Terminal input</label>
              <input disabled={control.data?.state !== 'active'} id="terminal-input" onChange={(event) => setTerminalInput(event.target.value)} placeholder="Take control to send input" value={terminalInput} />
              <button disabled={control.data?.state !== 'active'} type="submit">Send</button>
            </form>
          ) : null}
        </section>
        {current === undefined ? null : (
          <div className="terminal-controls">
            <span>
              {control.data?.authority === 'operator'
                ? `You have control until ${control.data.expires_at === undefined ? 'expiry' : new Date(control.data.expires_at).toLocaleTimeString()}. `
                : 'Ion has control. '}
              {humanize(control.data?.reconciliation ?? 'loading')}.{' '}
              {current.truncated ? 'Earlier output was truncated. ' : ''}
              {current.dropped_bytes > 0 ? `${current.dropped_bytes} bytes dropped. ` : ''}
              {replay.data?.gap === true ? 'Replay begins after an output gap.' : ''}
            </span>
            {current.status === 'running' ? <>
              {control.data?.state === 'active' ? <>
                <button className="quiet-button" onClick={() => { void changeControl('computer.control.renew') }} type="button">Renew control</button>
                <button className="quiet-button" onClick={() => { void changeControl('computer.control.release') }} type="button">Return control</button>
                <button className="quiet-button" onClick={() => { void terminalCommand('project.terminal.signal', { signal: 'INT' }) }} type="button">Interrupt</button>
                <button className="quiet-button danger-button" onClick={() => { void terminalCommand('project.terminal.cancel', {}) }} type="button">Cancel</button>
              </> : (
                <button className="quiet-button" disabled={control.data === undefined} onClick={() => { void changeControl('computer.control.acquire') }} type="button">Take control</button>
              )}
            </> : null}
          </div>
        )}
        {notice === undefined ? null : <p className="studio-notice" role="status">{notice}</p>}
      </PanelSection>
    </div>
  )
}

function PreviewPanel({ project }: { project: ProjectRecord }) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [revision, setRevision] = useState('HEAD')
  const [preview, setPreview] = useState<{ id: string; path: string; revision: string; state: string }>()
  const [notice, setNotice] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [viewport, setViewport] = useState<'desktop' | 'tablet' | 'mobile'>('desktop')
  const [dark, setDark] = useState(false)
  const [frameRevision, setFrameRevision] = useState(0)
  const [inspection, setInspection] = useState<RuntimeInspection>()
  const [selectedElement, setSelectedElement] = useState('')
  const [annotation, setAnnotation] = useState('')
  const [stylePath, setStylePath] = useState('')
  const [styleProperty, setStyleProperty] = useState('color')
  const [styleValue, setStyleValue] = useState('')
  const [serviceName, setServiceName] = useState('')
  const plan = useQuery({
    queryKey: ['studio', project.id, 'runtime-plan'],
    queryFn: async () => query<RuntimePlan>(operator, 'project.runtime.plan', { project_id: project.id }),
    retry: false,
  })
  const runtimes = useQuery({
    queryKey: ['studio', project.id, 'runtimes'],
    queryFn: async () => query<RuntimeState[]>(operator, 'project.runtime.list', { project_id: project.id }),
    retry: false,
    refetchInterval: 2_000,
  })
  useEffect(() => {
    if (serviceName === '' && plan.data?.default_service !== undefined) setServiceName(plan.data.default_service)
  }, [plan.data?.default_service, serviceName])
  const serviceNames = useMemo(() => Array.from(new Set([
    plan.data?.default_service ?? 'web',
    ...(runtimes.data ?? []).map((item) => item.name),
  ])).sort(), [plan.data?.default_service, runtimes.data])
  const activeService = serviceName.trim() || plan.data?.default_service || 'web'
  const serviceNameValid = /^[a-z][a-z0-9-]{0,39}$/.test(activeService)
  const runtime = (runtimes.data ?? []).find((item) => item.name === activeService)
  const runnable = plan.data?.commands.find((command) => command.kind === 'dev') ?? plan.data?.commands.find((command) => command.kind === 'start')
  const index = useQuery({
    queryKey: ['studio', project.id, 'index'],
    queryFn: async () => query<ProjectIndex>(operator, 'project.index.get', { project_id: project.id }),
    retry: false,
  })
  const styleFiles = (index.data?.files ?? []).filter((file) => /\.(css|scss|sass|less)$/i.test(file.path) && file.sha256 !== undefined)
  useEffect(() => {
    if (stylePath === '' && styleFiles[0] !== undefined) setStylePath(styleFiles[0].path)
  }, [styleFiles, stylePath])
  const runtimeCommand = async (operation: 'project.runtime.reload' | 'project.runtime.restart' | 'project.runtime.stop') => {
    setBusy(true)
    const response = await operator.command<RuntimeState>(operation, { project_id: project.id, name: runtime?.name ?? 'web' }, crypto.randomUUID())
    setBusy(false)
    setNotice(response.error?.message ?? `${humanize(operation.split('.').at(-1) ?? 'runtime')} completed.`)
    if (response.error === undefined) {
      if (operation === 'project.runtime.reload') setFrameRevision((value) => value + 1)
      await queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'runtimes'] })
    }
  }
  const startRuntime = async () => {
    if (plan.data === undefined || runnable === undefined) return
    setBusy(true)
    const response = await operator.command<RuntimeState>('project.runtime.start', {
      project_id: project.id,
      workspace_revision: project.workspace_revision,
      name: activeService,
      command_kind: runnable.kind,
      readiness_path: plan.data.readiness_path,
      readiness_seconds: 30,
    }, crypto.randomUUID())
    setBusy(false)
    setNotice(response.error?.message ?? 'The live preview passed readiness and is running.')
    await queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'runtimes'] })
  }
  const inspect = async () => {
    if (runtime === undefined) return
    setBusy(true)
    try {
      const dimensions = viewport === 'mobile' ? [390, 844] : viewport === 'tablet' ? [820, 1180] : [1440, 900]
      const result = await query<RuntimeInspection>(operator, 'project.runtime.inspect', {
        project_id: project.id, name: runtime.name, width: dimensions[0], height: dimensions[1], dark_mode: dark,
      })
      setInspection(result)
      setSelectedElement(result.elements[0]?.ref ?? '')
      setNotice('Screenshot, DOM, accessibility, console, and network evidence were captured.')
      await queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'runtimes'] })
    } catch (error) {
      setNotice(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  const saveAnnotation = async (event: FormEvent) => {
    event.preventDefault()
    const response = await operator.command<RuntimeState>('project.runtime.annotate', {
      project_id: project.id, name: runtime?.name ?? 'web', element_ref: selectedElement, body: annotation,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Visual annotation saved against the selected element.')
    if (response.error === undefined) {
      setAnnotation('')
      await queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'runtimes'] })
    }
  }
  const proposeStyle = async (event: FormEvent) => {
    event.preventDefault()
    const file = styleFiles.find((item) => item.path === stylePath)
    if (file?.sha256 === undefined) return
    const response = await operator.command<RuntimeState>('project.runtime.style.propose', {
      project_id: project.id, name: runtime?.name ?? 'web', element_ref: selectedElement,
      path: file.path, expected_sha256: file.sha256, declarations: { [styleProperty]: styleValue },
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'A revision-bound style proposal is ready for source review; no file was changed.')
    if (response.error === undefined) await queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'runtimes'] })
  }
  const startHistorical = async (event: FormEvent) => {
    event.preventDefault()
    const response = await operator.command<{ id: string; path: string; revision: string; state: string }>(
      'project.git.preview.start',
      { project_id: project.id, revision },
      crypto.randomUUID(),
    )
    if (response.error !== undefined || response.result === undefined) setNotice(response.error?.message ?? 'Historical preview could not start.')
    else { setPreview(response.result); setNotice('A read-only historical workspace is ready for inspection.') }
  }
  const close = async () => {
    if (preview === undefined) return
    const response = await operator.command('project.git.preview.close', { project_id: project.id, preview_id: preview.id }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Historical workspace closed without changing the active tree.')
    if (response.error === undefined) setPreview(undefined)
  }
  return (
    <div className="studio-panel-stack">
      <PanelSection kicker="LIVE RESULT" title="Preview">
        <div className="preview-runtime-bar">
          <div><StatusPill tone={runtime?.status === 'running' ? 'success' : runtime?.status === 'crashed' || runtime?.status === 'failed' ? 'danger' : 'quiet'}>{runtime?.status ?? 'Stopped'}</StatusPill><span>{plan.data?.stack ?? 'Detecting runtime'}{runtime?.port === undefined ? '' : ` · port ${String(runtime.port)}`}</span></div>
          <div className="preview-actions">
            <label className="sr-only" htmlFor="preview-service">Preview service</label>
            <input
              aria-label="Preview service"
              id="preview-service"
              list="preview-service-names"
              onChange={(event) => setServiceName(event.target.value)}
              pattern="[a-z][a-z0-9-]{0,39}"
              disabled={busy}
              value={serviceName}
            />
            <datalist id="preview-service-names">{serviceNames.map((name) => <option key={name} value={name} />)}</datalist>
            <select aria-label="Preview viewport" onChange={(event) => setViewport(event.target.value as typeof viewport)} value={viewport}><option value="desktop">Desktop</option><option value="tablet">Tablet</option><option value="mobile">Mobile</option></select>
            <button className="quiet-button" onClick={() => setDark((value) => !value)} type="button">{dark ? 'Light' : 'Dark'}</button>
            {runtime?.status === 'running' ? <>
              <button className="quiet-button" disabled={busy} onClick={() => { void runtimeCommand('project.runtime.reload') }} type="button">Reload</button>
              <button className="quiet-button" disabled={busy} onClick={() => { void inspect() }} type="button">Capture & inspect</button>
              <button className="quiet-button" disabled={busy} onClick={() => { void runtimeCommand('project.runtime.restart') }} type="button">Restart</button>
              <button className="quiet-button danger-button" disabled={busy} onClick={() => { void runtimeCommand('project.runtime.stop') }} type="button">Stop</button>
            </> : <button disabled={busy || runnable === undefined || !serviceNameValid} onClick={() => { void startRuntime() }} type="button">{busy ? 'Starting…' : 'Start preview'}</button>}
          </div>
        </div>
        {runtime?.status === 'running' ? (
          <div className={`preview-browser preview-${viewport}${dark ? ' preview-dark' : ''}`}>
            <div className="preview-toolbar"><span /><span className="preview-address">{runtime.preview_url}</span><a href={runtime.preview_url} rel="noreferrer" target="_blank">Open</a></div>
            <iframe key={`${runtime.id}-${String(frameRevision)}`} referrerPolicy="no-referrer" sandbox="allow-forms allow-same-origin allow-scripts" src={runtime.preview_url} title="Isolated project preview" />
          </div>
        ) : (
          <div className="preview-placeholder"><div><Icon name="workflow" /><strong>{runnable === undefined ? 'No safe start command was inferred' : 'Preview is ready to start'}</strong><p>{plan.data?.warnings?.[0] ?? runnable?.description ?? 'Start the inferred service to open an origin-isolated live result.'}</p></div></div>
        )}
        {runtime === undefined ? null : <p className="studio-notice"><strong>Next action:</strong> {runtime.next_action}</p>}
        {runtime?.logs === undefined || runtime.logs === '' ? null : <details className="technical-details"><summary>Runtime logs{runtime.logs_truncated ? ' (truncated)' : ''}</summary><pre><code>{runtime.logs}</code></pre></details>}
        {(runtime?.diagnostics ?? []).length === 0 ? null : <ul className="diagnostic-list">{runtime?.diagnostics?.map((diagnostic) => <li key={diagnostic.id}><StatusPill tone={diagnostic.severity === 'error' ? 'danger' : 'attention'}>{diagnostic.severity}</StatusPill><div><strong>{diagnostic.message}</strong><span>{diagnostic.source}{diagnostic.code === undefined ? '' : ` · ${diagnostic.code}`}{diagnostic.recurrence > 1 ? ` · repeated ${String(diagnostic.recurrence)} times` : ''}</span></div></li>)}</ul>}
      </PanelSection>
      {inspection === undefined ? null : (
        <PanelSection aside={<StatusPill tone={(inspection.accessibility ?? []).length === 0 ? 'success' : 'attention'}>{(inspection.accessibility ?? []).length} a11y findings</StatusPill>} kicker="BROWSER EVIDENCE" title="Inspect the result">
          <div className="preview-inspection-grid">
            <figure><img alt={`Screenshot of ${inspection.title || 'project preview'}`} src={inspection.screenshot_png} /><figcaption><code>{shortHash(inspection.screenshot_sha256)}</code> · {inspection.width}×{inspection.height} · {inspection.dark_mode ? 'dark' : 'light'}</figcaption></figure>
            <div className="preview-dom-inspector">
              <label>Element<select onChange={(event) => setSelectedElement(event.target.value)} value={selectedElement}>{inspection.elements.map((element) => <option key={element.ref} value={element.ref}>{element.ref} · {element.tag} · {element.name ?? element.text ?? element.placeholder ?? 'unnamed'}</option>)}</select></label>
              <PlainList values={(inspection.accessibility ?? []).map((finding) => `${finding.ref}: ${finding.message}`)} empty="No basic accessible-name findings in this capture." />
              <form onSubmit={saveAnnotation}><label>Annotation<textarea onChange={(event) => setAnnotation(event.target.value)} required rows={2} value={annotation} /></label><button disabled={selectedElement === ''} type="submit">Save annotation</button></form>
              <form onSubmit={proposeStyle}><label>Stylesheet<select onChange={(event) => setStylePath(event.target.value)} value={stylePath}>{styleFiles.map((file) => <option key={file.path}>{file.path}</option>)}</select></label><div className="style-proposal-fields"><label>Property<input onChange={(event) => setStyleProperty(event.target.value)} required value={styleProperty} /></label><label>Value<input onChange={(event) => setStyleValue(event.target.value)} required value={styleValue} /></label></div><button disabled={selectedElement === '' || styleFiles.length === 0} type="submit">Propose source change</button></form>
            </div>
          </div>
        </PanelSection>
      )}
      <PanelSection kicker="VERSION INSPECTION" title="Open a historical workspace">
        <p className="studio-leading-copy">Inspect an earlier Git revision in a detached workspace without checking out over active work.</p>
        {preview === undefined ? (
          <form className="inline-form" onSubmit={startHistorical}><label>Revision<input onChange={(event) => setRevision(event.target.value)} required value={revision} /></label><button type="submit">Prepare workspace</button></form>
        ) : (
          <div className="historical-preview"><div><strong>{preview.revision}</strong><span>{preview.path}</span></div><StatusPill tone="success">{humanize(preview.state)}</StatusPill><button className="quiet-button" onClick={() => { void close() }} type="button">Close</button></div>
        )}
        {notice === undefined ? null : <p className="studio-notice" role="status">{notice}</p>}
      </PanelSection>
    </div>
  )
}

function ProblemsPanel({ intent, project, setPanel }: { intent: StudioIntent | undefined; project: ProjectRecord; setPanel(panel: StudioPanel): void }) {
  const operator = useOperator()
  const [notice, setNotice] = useState<string>()
  const index = useQuery({
    queryKey: ['studio', project.id, 'index'],
    queryFn: async () => query<ProjectIndex>(operator, 'project.index.get', { project_id: project.id }),
    retry: false,
  })
  const indexedDiagnostics = ((index.data as unknown as { diagnostics?: Array<{
    path: string; line: number; column?: number; severity: string; code?: string; message: string; source: string
  }> } | undefined)?.diagnostics ?? [])
  const runtimeProblems = useQuery({
    queryKey: ['studio', project.id, 'runtime-problems'],
    queryFn: async () => query<RuntimeProblem[]>(operator, 'project.runtime.problems', { project_id: project.id }),
    retry: false,
    refetchInterval: 2_000,
  })
  const verificationRuns = useQuery({
    queryKey: ['studio', project.id, 'verification-runs'],
    queryFn: async () => query<ProjectVerificationRun[]>(operator, 'project.verification.runs', { project_id: project.id }),
    retry: false,
    refetchInterval: 2_000,
  })
  const latestVerification = verificationRuns.data?.at(-1)
  const verificationDiagnostics = (latestVerification?.results ?? [])
    .filter((result) => result.status !== 'passed' && result.status !== 'waived')
    .map((result) => ({
      id: result.failure_signature ?? `${latestVerification?.id ?? 'verification'}-${result.gate_id}`,
      source: 'verification',
      severity: 'error',
      code: result.gate_id,
      message: result.unavailable_reason ?? result.logs?.trim().split('\n').at(-1) ?? `${humanize(result.kind)} verification ${humanize(result.status)}`,
      path: '',
      line: 0,
      column: undefined,
      recurrence: 1,
      causal_evidence: result.failure_signature === undefined ? [] : [result.failure_signature],
    }))
  const diagnostics = [
    ...indexedDiagnostics.map((item, itemIndex) => ({ ...item, id: `${item.source}-${item.path}-${String(item.line)}-${String(itemIndex)}`, recurrence: 1, column: item.column })),
    ...(runtimeProblems.data ?? []).map((item) => ({ ...item, path: item.path ?? '', line: item.line ?? 0, column: item.column })),
    ...verificationDiagnostics,
  ]
  const askToFix = async (item: typeof diagnostics[number]) => {
    let sessionID = operator.sessionID
    if (sessionID === undefined) {
      const created = await operator.command<{ id: string }>('session.create', {}, crypto.randomUUID())
      if (created.error !== undefined || created.result?.id === undefined) {
        setNotice(created.error?.message ?? 'A repair conversation could not be started.')
        return
      }
      sessionID = created.result.id
      operator.setSessionID(sessionID)
    }
    const active = intent?.proposals.find((proposal) => proposal.id === intent.active_proposal_id) ?? intent?.proposals.at(-1)
    const criterion = active?.delta.acceptance_criteria[0]?.id ?? 'active project acceptance criterion'
    const response = await operator.command('turn.submit', {
      content: `Diagnose and propose a bounded fix for project ${project.id}, linked to ${criterion}. Problem signature: ${item.id}. Evidence: [${item.severity}] ${item.source}${item.code === undefined ? '' : ` ${item.code}`}: ${item.message}${item.path === '' ? '' : ` at ${item.path}:${String(item.line)}`}. Inspect current source and rerun the narrowest relevant verification; do not claim success without fresh evidence.`,
      surface: 'studio',
      project_id: project.id,
    }, crypto.randomUUID(), { session_id: sessionID })
    setNotice(response.error?.message ?? 'The bounded repair request is now in the project conversation.')
  }
  const severity = (value: string) => value.toLowerCase() === 'error' ? 'danger' : value.toLowerCase() === 'warning' ? 'attention' : 'quiet'
  return (
    <div className="studio-panel-stack">
      <PanelSection
        aside={index.data === undefined ? undefined : <StatusPill tone={diagnostics.some((item) => item.severity.toLowerCase() === 'error') ? 'danger' : 'success'}>{diagnostics.length} {diagnostics.length === 1 ? 'problem' : 'problems'}</StatusPill>}
        kicker="DIAGNOSTICS"
        title="Problems"
      >
        {index.isPending && verificationRuns.isPending ? <StudioSkeleton /> : (index.isError || index.data === undefined) && diagnostics.length === 0 ? (
          <><StudioEmpty title="Diagnostics are not indexed" detail="Build the project index from Code to collect revision-bound diagnostics." /><button onClick={() => setPanel('code')} type="button">Open Code</button></>
        ) : diagnostics.length === 0 ? (
          <StudioEmpty title="No recorded problems" detail="The current index contains no compiler, linter, test, accessibility, or security diagnostics. This is not a substitute for running verification." />
        ) : (
          <ol className="diagnostic-list">
            {diagnostics.map((item) => (
              <li key={item.id}>
                <StatusPill tone={severity(item.severity) as 'danger' | 'attention' | 'quiet'}>{item.severity}</StatusPill>
                <div><strong>{item.message}</strong><span>{item.path === '' ? item.source : `${item.path}:${String(item.line)}${item.column === undefined ? '' : `:${String(item.column)}`} · ${item.source}`}{item.code === undefined ? '' : ` · ${item.code}`}{item.recurrence > 1 ? ` · repeated ${String(item.recurrence)} times` : ''}</span></div>
                <button className="quiet-button" onClick={() => { void askToFix(item) }} type="button">Ask Ion to fix</button>
              </li>
            ))}
          </ol>
        )}
        {notice === undefined ? null : <p className="studio-notice" role="status">{notice}</p>}
      </PanelSection>
    </div>
  )
}

function TestsPanel({
  brief,
  intent,
  project,
  setPanel,
}: {
  brief: WorkBrief | undefined
  intent: StudioIntent | undefined
  project: ProjectRecord
  setPanel(panel: StudioPanel): void
}) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string>()
  const [waiverGate, setWaiverGate] = useState('')
  const [waiverReason, setWaiverReason] = useState('')
  const [waiverRisk, setWaiverRisk] = useState('')
  const toolchain = useQuery({
    queryKey: ['studio', project.id, 'toolchain'],
    queryFn: async () => query<ToolchainReport>(operator, 'project.toolchain.get', { project_id: project.id }),
    retry: false,
  })
  const completion = useQuery({
    queryKey: ['studio', project.id, 'completion', intent?.id],
    queryFn: async () => query<Record<string, unknown>>(operator, 'studio.completion.check', { intent_id: intent?.id }),
    enabled: intent?.id !== undefined,
    retry: false,
  })
  const manifest = useQuery({
    queryKey: ['studio', project.id, 'verification-manifest'],
    queryFn: async () => query<ProjectVerificationManifest>(operator, 'project.verification.manifest.get', { project_id: project.id }),
    retry: false,
  })
  const runs = useQuery({
    queryKey: ['studio', project.id, 'verification-runs'],
    queryFn: async () => query<ProjectVerificationRun[]>(operator, 'project.verification.runs', { project_id: project.id }),
    retry: false,
    refetchInterval: 2_000,
  })
  const waivers = useQuery({
    queryKey: ['studio', project.id, 'verification-waivers'],
    queryFn: async () => query<ProjectVerificationWaiver[]>(operator, 'project.verification.waivers', { project_id: project.id }),
    retry: false,
  })
  const active = intent?.proposals.find((proposal) => proposal.id === intent.active_proposal_id) ?? intent?.proposals.at(-1)
  const commands = active?.delta.verification_commands ?? brief?.contract?.verification_required ?? []
  const verified = brief?.verified_criteria?.length ?? 0
  const unverified = brief?.unverified_criteria?.length ?? active?.delta.acceptance_criteria.length ?? 0
  const criteria = (active?.delta.acceptance_criteria ?? brief?.contract?.done_criteria ?? [])
    .flatMap((criterion) => criterion.id === undefined || criterion.description === undefined ? [] : [{
      id: criterion.id,
      description: criterion.description,
      kinds: [] as string[],
    }])
  const latest = runs.data?.at(-1)
  const refreshVerification = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'verification-manifest'] }),
      queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'verification-runs'] }),
      queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'verification-waivers'] }),
    ])
  }
  const deriveManifest = async () => {
    setBusy(true)
    const response = await operator.command<ProjectVerificationManifest>('project.verification.manifest.derive', {
      project_id: project.id,
      workspace_revision: project.workspace_revision,
      criteria,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Verification manifest derived from the accepted criteria and inspected repository.')
    await refreshVerification()
    setBusy(false)
  }
  const runFull = async () => {
    if (manifest.data === undefined) return
    setBusy(true)
    const response = await operator.command<ProjectVerificationRun>('project.verification.run', {
      project_id: project.id,
      manifest_id: manifest.data.id,
      full: true,
      max_attempts: 3,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? `Verification finished: ${humanize(response.result?.status ?? 'unknown')}.`)
    await refreshVerification()
    setBusy(false)
  }
  const createWaiver = async (event: FormEvent) => {
    event.preventDefault()
    if (manifest.data === undefined) return
    const gate = manifest.data.gates.find((candidate) => candidate.id === waiverGate)
    if (gate === undefined) return
    setBusy(true)
    const response = await operator.command<ProjectVerificationWaiver>('project.verification.waiver.create', {
      project_id: project.id,
      manifest_id: manifest.data.id,
      gate_ids: [gate.id],
      criteria: gate.criteria,
      reason: waiverReason,
      risk: waiverRisk,
      expires_at: new Date(Date.now() + 24 * 60 * 60 * 1_000).toISOString(),
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'The 24-hour waiver is visible and its criteria remain uncovered.')
    if (response.error === undefined) {
      setWaiverReason('')
      setWaiverRisk('')
    }
    await refreshVerification()
    setBusy(false)
  }
  const activeWaivers = (waivers.data ?? []).filter((waiver) => waiver.revoked_at === undefined && new Date(waiver.expires_at).getTime() > Date.now())
  const manifestStale = manifest.data !== undefined &&
    manifest.data.workspace_revision !== project.workspace_revision
  return (
    <div className="studio-panel-stack">
      <PanelSection
        aside={<StatusPill tone={unverified === 0 && verified > 0 ? 'success' : 'attention'}>{verified} verified · {unverified} open</StatusPill>}
        kicker="VERIFICATION"
        title="Tests"
      >
        <div className="verification-progress" aria-label={`${brief?.completion_percentage ?? 0}% verified`}>
          <span style={{ width: `${Math.max(0, Math.min(100, brief?.completion_percentage ?? 0))}%` }} />
        </div>
        <PlainList values={commands} empty="No verification commands have been accepted yet." />
        <div className="studio-plan-actions">
          {manifest.data === undefined || manifestStale ? (
            <button disabled={busy || criteria.length === 0} onClick={() => { void deriveManifest() }} type="button">
              {busy ? 'Preparing…' : manifestStale ? 'Refresh verification' : 'Prepare verification'}
            </button>
          ) : (
            <button disabled={busy} onClick={() => { void runFull() }} type="button">{busy ? 'Running…' : 'Run required gates'}</button>
          )}
          <button className="quiet-button" onClick={() => setPanel('terminal')} type="button">Open terminal</button>
          {completion.data === undefined ? null : <details className="technical-details"><summary>Completion evidence</summary><pre><code>{JSON.stringify(completion.data, null, 2)}</code></pre></details>}
        </div>
        {manifest.data === undefined ? (
          <StudioEmpty title="No verification manifest" detail={criteria.length === 0 ? 'Accept a project specification before deriving release gates.' : 'Prepare verification to inspect repository commands and bind them to the accepted criteria.'} />
        ) : manifestStale ? (
          <StudioEmpty title="Verification needs refresh" detail="The project changed after this manifest was derived. Refresh it before running gates so evidence binds to the current revision." />
        ) : (
          <>
            <ol className="diagnostic-list">
              {manifest.data.gates.map((gate) => {
                const result = latest?.results.find((candidate) => candidate.gate_id === gate.id)
                const status = result?.status ?? (gate.available ? 'not run' : 'unavailable')
                return <li key={gate.id}><StatusPill tone={status === 'passed' ? 'success' : status === 'not run' ? 'quiet' : status === 'waived' ? 'attention' : 'danger'}>{humanize(status)}</StatusPill><div><strong>{humanize(gate.kind)}</strong><span>{gate.argv?.join(' ') ?? gate.unavailable_reason ?? 'No executable command'} · {gate.criteria.length} criteria</span></div></li>
              })}
            </ol>
            <p className="studio-muted">{latest === undefined ? 'No revision-bound run has been recorded.' : `${latest.criteria_covered.length} covered · ${latest.uncovered_criteria.length} uncovered · ${latest.repair.reason}`}</p>
          </>
        )}
        {notice === undefined ? null : <p className="studio-notice" role="status">{notice}</p>}
      </PanelSection>
      {manifest.data === undefined ? null : (
        <PanelSection aside={<StatusPill tone={activeWaivers.length === 0 ? 'success' : 'attention'}>{activeWaivers.length} active</StatusPill>} kicker="WAIVERS" title="Explicit uncovered risk">
          <PlainList values={activeWaivers.map((waiver) => `${waiver.gate_ids.join(', ')} · ${waiver.reason} · expires ${new Date(waiver.expires_at).toLocaleString()}`)} empty="No active verification waivers." />
          <details className="technical-details">
            <summary>Authorize a 24-hour waiver</summary>
            <form onSubmit={createWaiver}>
              <label>Gate<select onChange={(event) => setWaiverGate(event.target.value)} required value={waiverGate}><option value="">Select a gate</option>{manifest.data.gates.map((gate) => <option key={gate.id} value={gate.id}>{humanize(gate.id)}</option>)}</select></label>
              <label>Reason<input onChange={(event) => setWaiverReason(event.target.value)} required value={waiverReason} /></label>
              <label>Risk<input onChange={(event) => setWaiverRisk(event.target.value)} required value={waiverRisk} /></label>
              <button disabled={busy || waiverGate === ''} type="submit">Authorize waiver</button>
            </form>
          </details>
        </PanelSection>
      )}
      <PanelSection kicker="TOOLCHAIN" title="Detected build environment">
        {toolchain.isPending ? <StudioSkeleton /> : toolchain.isError || toolchain.data === undefined ? (
          <StudioEmpty title="Toolchain discovery is unavailable" detail={errorMessage(toolchain.error)} />
        ) : (
          <div className="toolchain-grid">
            {toolchain.data.runtimes.map((runtime) => (
              <div key={runtime.name} data-available={runtime.available}><span>{runtime.name}</span><strong>{runtime.available ? runtime.version ?? 'Available' : 'Not found'}</strong></div>
            ))}
          </div>
        )}
      </PanelSection>
    </div>
  )
}

function SecurityPanel({ project }: { project: ProjectRecord }) {
  const operator = useOperator()
  const index = useQuery({
    queryKey: ['studio', project.id, 'index'],
    queryFn: async () => query<ProjectIndex>(operator, 'project.index.get', { project_id: project.id }),
    retry: false,
  })
  const capabilities = useQuery({
    queryKey: ['studio', 'workspace-capabilities'],
    queryFn: async () => query<{ hosts: Array<{
      kind: string; available: boolean; non_root: boolean; root_confined: boolean; network: { mode: string }; authority_disclosure?: string
    }> }>(operator, 'workspace.capabilities', {}),
    retry: false,
  })
  const findings = (index.data?.files ?? []).flatMap((file) => file.secret_findings ?? [])
  const host = capabilities.data?.hosts.find((item) => item.kind === project.host)
  return (
    <div className="studio-panel-stack">
      <div className="security-summary-grid">
        <SecurityFact label="Repository trust" value={humanize(project.trust)} good={project.trust === 'trusted' || project.trust === 'reviewed'} />
        <SecurityFact label="Root confined" value={host?.root_confined === true ? 'Enforced' : 'Unconfirmed'} good={host?.root_confined === true} />
        <SecurityFact label="Process user" value={host?.non_root === true ? 'Non-root' : 'Unconfirmed'} good={host?.non_root === true} />
        <SecurityFact label="Default network" value={host?.network.mode ?? 'Unknown'} good={host?.network.mode === 'deny'} />
      </div>
      <PanelSection
        aside={<StatusPill tone={findings.length === 0 ? 'success' : 'danger'}>{findings.length} {findings.length === 1 ? 'finding' : 'findings'}</StatusPill>}
        kicker="SECRET SCAN"
        title="Protected content"
      >
        {index.isError || index.data === undefined ? (
          <StudioEmpty title="Security findings are not indexed" detail="Refresh the code index before relying on this view. Repository text remains untrusted regardless of scan state." />
        ) : findings.length === 0 ? (
          <StudioEmpty title="No secret-shaped content recorded" detail="The current index has no recorded secret findings. Private values still belong only in write-only vault grants." />
        ) : (
          <ul className="security-findings">{findings.map((finding, index) => <li key={`${finding.path}-${String(finding.line)}-${String(index)}`}><Icon name="shield" /><div><strong>{humanize(finding.kind)}</strong><span>{finding.path}:{finding.line}</span></div></li>)}</ul>
        )}
      </PanelSection>
      <PanelSection kicker="AUTHORITY" title="Workspace boundary">
        <p className="studio-leading-copy">{host?.authority_disclosure ?? 'The selected host has not returned an authority disclosure.'}</p>
        <p className="studio-muted">Repository instructions and issue text are treated as data. They cannot override system safety, user authority, approval requirements, or project confinement.</p>
      </PanelSection>
    </div>
  )
}

interface ResourcePlanView {
  id: string
  classification: string
  estimated_cost_cents: number
  actions: string[]
  desired: { name: string; kind: string; environment: string }
}

interface MigrationPlanView {
  id: string
  classification: string
  dry_run_passed: boolean
  destructive_findings: string[]
  schema_before: string[]
  schema_after: string[]
}

interface DeploymentPlanView {
  id: string
  classification: string
  artifact: { sha256: string; size_bytes: number }
  actions: string[]
  release_version: string
}

function DataPanel({ project }: { project: ProjectRecord }) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string>()
  const [resourceName, setResourceName] = useState('primary-db')
  const [resourceKind, setResourceKind] = useState('database')
  const [resourceEnvironment, setResourceEnvironment] = useState('development')
  const [resourcePlan, setResourcePlan] = useState<ResourcePlanView>()
  const [environment, setEnvironment] = useState('development')
  const [variableName, setVariableName] = useState('')
  const [variableReference, setVariableReference] = useState('')
  const [variableKind, setVariableKind] = useState('secret_reference')
  const [databasePath, setDatabasePath] = useState('')
  const [migrationEnvironment, setMigrationEnvironment] = useState('development')
  const [migrationSQL, setMigrationSQL] = useState('')
  const [migrationRollback, setMigrationRollback] = useState('')
  const [migrationPlan, setMigrationPlan] = useState<MigrationPlanView>()
  const delivery = useQuery({
    queryKey: ['studio', project.id, 'delivery'],
    queryFn: async () => query<ProjectDeliverySnapshot>(operator, 'project.delivery.get', { project_id: project.id }),
    retry: false,
    refetchInterval: 3_000,
  })
  const refresh = async () => queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'delivery'] })
  const planResource = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    const response = await operator.command('project.resource.plan', {
      project_id: project.id,
      workspace_revision: project.workspace_revision,
      desired: {
        name: resourceName,
        kind: resourceKind,
        provider: 'local',
        environment: resourceEnvironment,
        capabilities: resourceKind === 'database' ? ['schema', 'migration', 'backup', 'rollback'] : ['export'],
        ownership: 'ion_managed',
        data_risk: resourceKind === 'database' ? 'customer_data' : 'customer_files',
        engine: resourceKind === 'database' ? 'sqlite' : '',
        monthly_cost_limit_cents: 0,
      },
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Resource plan is ready for review.')
    if (response.error === undefined) setResourcePlan(response.result as unknown as ResourcePlanView)
    setBusy(false)
  }
  const applyResource = async () => {
    if (resourcePlan === undefined) return
    setBusy(true)
    const response = await operator.command('project.resource.apply', {
      project_id: project.id,
      plan_id: resourcePlan.id,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Resource provisioned with current provider evidence.')
    if (response.error === undefined) {
      setResourcePlan(undefined)
      await refresh()
    }
    setBusy(false)
  }
  const saveEnvironment = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    const response = await operator.command('project.environment.put', {
      project_id: project.id,
      environment,
      variables: variableName === '' ? [] : [{
        name: variableName,
        kind: variableKind,
        reference: variableReference,
        required: true,
      }],
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Versioned environment schema saved without storing a value.')
    if (response.error === undefined) {
      setVariableName('')
      setVariableReference('')
      await refresh()
    }
    setBusy(false)
  }
  const planMigration = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    const response = await operator.command('project.migration.plan', {
      project_id: project.id,
      workspace_revision: project.workspace_revision,
      environment: migrationEnvironment,
      database_path: databasePath,
      steps: [{ id: 'reviewed_change', sql: migrationSQL, rollback: migrationRollback }],
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Migration dry-run completed against an isolated database copy.')
    if (response.error === undefined) setMigrationPlan(response.result as unknown as MigrationPlanView)
    setBusy(false)
  }
  const applyMigration = async () => {
    if (migrationPlan === undefined) return
    setBusy(true)
    const response = await operator.command('project.migration.apply', {
      project_id: project.id,
      plan_id: migrationPlan.id,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Migration applied with backup and rollback evidence.')
    if (response.error === undefined) {
      setMigrationPlan(undefined)
      await refresh()
    }
    setBusy(false)
  }
  const rollbackMigration = async (receiptID: string) => {
    setBusy(true)
    const response = await operator.command('project.migration.rollback', {
      project_id: project.id,
      receipt_id: receiptID,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Database restored from verified backup evidence.')
    await refresh()
    setBusy(false)
  }
  return (
    <div className="studio-panel-stack">
      <PanelSection
        aside={<StatusPill tone={(delivery.data?.resources.length ?? 0) > 0 ? 'success' : 'quiet'}>{delivery.data?.resources.length ?? 0} connected</StatusPill>}
        kicker="PROJECT RESOURCES"
        title="Data and services"
      >
        {delivery.isPending ? <StudioSkeleton /> : delivery.isError || delivery.data === undefined ? (
          <StudioEmpty title="Resource service is unavailable" detail={errorMessage(delivery.error)} />
        ) : delivery.data.resources.length === 0 ? (
          <StudioEmpty title="No resource is connected" detail="Plans are provider-neutral. A resource appears here only after its adapter returns direct ready evidence." />
        ) : (
          <ol className="diagnostic-list">{delivery.data.resources.map((resource) => (
            <li key={resource.id}><StatusPill tone={resource.state === 'ready' ? 'success' : 'danger'}>{humanize(resource.state)}</StatusPill><div><strong>{humanize(resource.provider)} · {humanize(resource.environment)}</strong><span>{resource.endpoint ?? resource.external_id ?? 'No endpoint'} · {resource.actual_cost_cents}¢</span></div></li>
          ))}</ol>
        )}
        <details className="technical-details">
          <summary>Plan a resource</summary>
          <form onSubmit={planResource}>
            <label>Name<input onChange={(event) => setResourceName(event.target.value)} pattern="[a-z][a-z0-9_-]{0,62}" required value={resourceName} /></label>
            <label>Kind<select onChange={(event) => setResourceKind(event.target.value)} value={resourceKind}><option value="database">Database</option><option value="object_storage">Object storage</option><option value="queue">Queue</option><option value="analytics">Analytics</option></select></label>
            <label>Environment<select onChange={(event) => setResourceEnvironment(event.target.value)} value={resourceEnvironment}><option value="development">Development</option><option value="test">Test</option><option value="preview">Preview</option><option value="staging">Staging</option><option value="production">Production</option></select></label>
            <button disabled={busy} type="submit">{busy ? 'Planning…' : 'Prepare plan'}</button>
          </form>
        </details>
        {resourcePlan === undefined ? null : (
          <div className="studio-notice" role="status"><strong>{humanize(resourcePlan.classification)} plan · {resourcePlan.estimated_cost_cents}¢</strong><PlainList values={resourcePlan.actions} empty="No provider actions." /><button disabled={busy} onClick={() => { void applyResource() }} type="button">Provision reviewed plan</button></div>
        )}
      </PanelSection>
      <PanelSection
        aside={<StatusPill tone={(delivery.data?.environments.length ?? 0) > 0 ? 'success' : 'quiet'}>{delivery.data?.environments.length ?? 0} versions</StatusPill>}
        kicker="ENVIRONMENTS"
        title="Write-only configuration"
      >
        <PlainList values={(delivery.data?.environments ?? []).map((schema) => `${humanize(schema.environment)} v${String(schema.revision)} · ${schema.variables.length} references`)} empty="No environment schema has been saved." />
        <details className="technical-details">
          <summary>Save a schema version</summary>
          <form onSubmit={saveEnvironment}>
            <label>Environment<select onChange={(event) => setEnvironment(event.target.value)} value={environment}><option value="development">Development</option><option value="test">Test</option><option value="preview">Preview</option><option value="staging">Staging</option><option value="production">Production</option></select></label>
            <label>Variable name<input onChange={(event) => setVariableName(event.target.value.toUpperCase())} placeholder="DATABASE_URL" value={variableName} /></label>
            <label>Reference kind<select onChange={(event) => setVariableKind(event.target.value)} value={variableKind}><option value="secret_reference">Vault secret</option><option value="config_reference">Config reference</option></select></label>
            <label>Write-only reference<input onChange={(event) => setVariableReference(event.target.value)} placeholder={variableKind === 'secret_reference' ? 'vault://projects/database-url' : 'config://projects/public-origin'} value={variableReference} /></label>
            <button disabled={busy} type="submit">Save schema</button>
          </form>
        </details>
      </PanelSection>
      <PanelSection
        aside={<StatusPill tone={(delivery.data?.migrations.length ?? 0) > 0 ? 'success' : 'quiet'}>{delivery.data?.migrations.length ?? 0} recorded</StatusPill>}
        kicker="MIGRATIONS"
        title="Dry-run, backup, and rollback"
      >
        {(delivery.data?.migrations.length ?? 0) === 0 ? <StudioEmpty title="No migration evidence" detail="Ion will inspect and dry-run SQL against an isolated database copy before any apply action." /> : (
          <ol className="diagnostic-list">{delivery.data?.migrations.map((migration) => (
            <li key={migration.id}><StatusPill tone={migration.state === 'applied' ? 'success' : 'attention'}>{humanize(migration.state)}</StatusPill><div><strong>{humanize(migration.environment)}</strong><span>{migration.schema_after.length} schema records · backup {migration.backup_sha256.slice(0, 12)}</span></div>{migration.rolled_back_at === undefined ? <button className="quiet-button" disabled={busy} onClick={() => { void rollbackMigration(migration.id) }} type="button">Rollback</button> : null}</li>
          ))}</ol>
        )}
        <details className="technical-details">
          <summary>Plan a SQLite migration</summary>
          <form onSubmit={planMigration}>
            <label>Environment<select onChange={(event) => setMigrationEnvironment(event.target.value)} value={migrationEnvironment}><option value="development">Development</option><option value="test">Test</option><option value="preview">Preview</option><option value="staging">Staging</option><option value="production">Production</option></select></label>
            <label>Database path<input onChange={(event) => setDatabasePath(event.target.value)} required value={databasePath} /></label>
            <label>Forward SQL<textarea onChange={(event) => setMigrationSQL(event.target.value)} required value={migrationSQL} /></label>
            <label>Rollback SQL<textarea onChange={(event) => setMigrationRollback(event.target.value)} required value={migrationRollback} /></label>
            <button disabled={busy} type="submit">{busy ? 'Checking…' : 'Run isolated dry-run'}</button>
          </form>
        </details>
        {migrationPlan === undefined ? null : (
          <div className="studio-notice" role="status"><strong>{migrationPlan.dry_run_passed ? 'Dry-run passed' : 'Dry-run incomplete'} · {humanize(migrationPlan.classification)}</strong><PlainList values={migrationPlan.destructive_findings} empty="No destructive SQL pattern was detected." /><button disabled={busy} onClick={() => { void applyMigration() }} type="button">Apply reviewed migration</button></div>
        )}
        {notice === undefined ? null : <p className="studio-notice" role="status">{notice}</p>}
      </PanelSection>
    </div>
  )
}

function DeployPanel({ project }: { project: ProjectRecord }) {
  const operator = useOperator()
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string>()
  const [environment, setEnvironment] = useState('staging')
  const [version, setVersion] = useState('0.1.0')
  const [healthPath, setHealthPath] = useState('/')
  const [deploymentPlan, setDeploymentPlan] = useState<DeploymentPlanView>()
  const [releaseNotes, setReleaseNotes] = useState('')
  const [changelog, setChangelog] = useState('')
  const delivery = useQuery({
    queryKey: ['studio', project.id, 'delivery'],
    queryFn: async () => query<ProjectDeliverySnapshot>(operator, 'project.delivery.get', { project_id: project.id }),
    retry: false,
    refetchInterval: 3_000,
  })
  const ci = useQuery({
    queryKey: ['studio', project.id, 'ci-patch'],
    queryFn: async () => query<ProjectCIPatchPlan>(operator, 'project.ci.patch.plan', { project_id: project.id }),
    retry: false,
  })
  const refresh = async () => queryClient.invalidateQueries({ queryKey: ['studio', project.id, 'delivery'] })
  const planDeployment = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    const response = await operator.command('project.deployment.plan', {
      project_id: project.id,
      workspace_revision: project.workspace_revision,
      environment,
      provider: 'local_staging',
      health_path: healthPath,
      version,
      cost_limit_cents: 0,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Immutable deployment plan is ready for review.')
    if (response.error === undefined) setDeploymentPlan(response.result as unknown as DeploymentPlanView)
    setBusy(false)
  }
  const applyDeployment = async () => {
    if (deploymentPlan === undefined) return
    setBusy(true)
    const response = await operator.command('project.deployment.apply', {
      project_id: project.id,
      plan_id: deploymentPlan.id,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Deployment is healthy from a direct service check.')
    if (response.error === undefined) {
      setDeploymentPlan(undefined)
      await refresh()
    }
    setBusy(false)
  }
  const reconcile = async (receiptID: string) => {
    setBusy(true)
    const response = await operator.command('project.deployment.reconcile', {
      project_id: project.id,
      receipt_id: receiptID,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Deployment reconciled from provider truth.')
    await refresh()
    setBusy(false)
  }
  const rollback = async (receiptID: string) => {
    setBusy(true)
    const response = await operator.command('project.deployment.rollback', {
      project_id: project.id,
      receipt_id: receiptID,
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Previous immutable release restored and health checked.')
    await refresh()
    setBusy(false)
  }
  const prepareRelease = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    const response = await operator.command('project.release.prepare', {
      project_id: project.id,
      release_version: version,
      notes: releaseNotes === '' ? [] : [releaseNotes],
      changelog: changelog === '' ? [] : [changelog],
    }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Release readiness refreshed from deployment, verification, and DNS evidence.')
    await refresh()
    setBusy(false)
  }
  const portableExport = async () => {
    setBusy(true)
    const response = await operator.command('project.portable.export', { project_id: project.id }, crypto.randomUUID())
    setNotice(response.error?.message ?? 'Portable source, data, config schema, and artifact export created.')
    await refresh()
    setBusy(false)
  }
  return (
    <div className="studio-panel-stack">
      <PanelSection
        aside={<StatusPill tone={(delivery.data?.deployments.length ?? 0) > 0 ? 'success' : 'quiet'}>{delivery.data?.deployments.length ?? 0} releases</StatusPill>}
        kicker="DEPLOYMENTS"
        title="Preview, staging, and production"
      >
        {delivery.isPending ? <StudioSkeleton /> : delivery.isError || delivery.data === undefined ? (
          <StudioEmpty title="Deployment service is unavailable" detail={errorMessage(delivery.error)} />
        ) : delivery.data.deployments.length === 0 ? (
          <StudioEmpty title="No deployment evidence" detail="Availability is shown only after the provider returns a version, URL, direct health result, cost, and rollback evidence." />
        ) : (
          <ol className="diagnostic-list">{delivery.data.deployments.slice().reverse().map((deployment) => (
            <li key={deployment.id}><StatusPill tone={deployment.health === 'passing' ? 'success' : deployment.state === 'outcome_unknown' ? 'attention' : 'danger'}>{humanize(deployment.state)}</StatusPill><div><strong>{deployment.release_version} · {humanize(deployment.environment)}</strong><span>{deployment.url ?? 'No URL'} · {deployment.actual_cost_cents}¢ · {deployment.artifact_sha256.slice(0, 12)}</span></div><button className="quiet-button" disabled={busy} onClick={() => { void reconcile(deployment.id) }} type="button">Reconcile</button>{deployment.previous_receipt === undefined || deployment.rolled_back_at !== undefined ? null : <button className="quiet-button" disabled={busy} onClick={() => { void rollback(deployment.id) }} type="button">Rollback</button>}</li>
          ))}</ol>
        )}
        <details className="technical-details">
          <summary>Prepare a deployment</summary>
          <form onSubmit={planDeployment}>
            <label>Environment<select onChange={(event) => setEnvironment(event.target.value)} value={environment}><option value="preview">Preview</option><option value="staging">Staging</option><option value="production">Production</option></select></label>
            <label>Version<input onChange={(event) => setVersion(event.target.value)} required value={version} /></label>
            <label>Health path<input onChange={(event) => setHealthPath(event.target.value)} required value={healthPath} /></label>
            <button disabled={busy} type="submit">{busy ? 'Building…' : 'Build immutable plan'}</button>
          </form>
        </details>
        {deploymentPlan === undefined ? null : (
          <div className="studio-notice" role="status"><strong>{humanize(deploymentPlan.classification)} plan · {deploymentPlan.artifact.size_bytes} bytes</strong><PlainList values={deploymentPlan.actions} empty="No provider actions." /><button disabled={busy} onClick={() => { void applyDeployment() }} type="button">Deploy reviewed artifact</button></div>
        )}
      </PanelSection>
      <PanelSection
        aside={<StatusPill tone={delivery.data?.release?.ready === true ? 'success' : 'attention'}>{delivery.data?.release?.ready === true ? 'Ready' : 'Evidence open'}</StatusPill>}
        kicker="RELEASE"
        title="Readiness and portable handoff"
      >
        {delivery.data?.release === undefined ? <StudioEmpty title="No release review" detail="Prepare release notes to check revision-bound verification, direct health, DNS, and portable export evidence." /> : (
          <><PlainList values={delivery.data.release.unmet} empty="Every required release signal is current." /><p className="studio-muted">DNS: {humanize(delivery.data.release.dns_state)} · export: {delivery.data.release.portable_export ?? 'not created'}</p></>
        )}
        <form onSubmit={prepareRelease}>
          <label>Release notes<textarea onChange={(event) => setReleaseNotes(event.target.value)} required value={releaseNotes} /></label>
          <label>Changelog<textarea onChange={(event) => setChangelog(event.target.value)} required value={changelog} /></label>
          <div className="studio-plan-actions"><button disabled={busy} type="submit">Review release</button><button className="quiet-button" disabled={busy} onClick={() => { void portableExport() }} type="button">Create portable export</button></div>
        </form>
      </PanelSection>
      <PanelSection
        aside={<StatusPill tone={ci.data?.review_required === true ? 'attention' : 'quiet'}>{ci.data?.review_required === true ? 'Review required' : 'Unavailable'}</StatusPill>}
        kicker="CI"
        title="Reviewed workflow patch"
      >
        {ci.isError || ci.data === undefined ? <StudioEmpty title="No CI patch is proposed" detail={errorMessage(ci.error)} /> : <><p className="studio-leading-copy">{ci.data.path}</p><details className="technical-details"><summary>Inspect proposed workflow</summary><pre><code>{ci.data.content}</code></pre></details></>}
        {notice === undefined ? null : <p className="studio-notice" role="status">{notice}</p>}
      </PanelSection>
    </div>
  )
}

function WorkBriefCard({ brief, loading }: { brief: WorkBrief | undefined; loading: boolean }) {
  const percent = brief?.completion_percentage ?? 0
  return (
    <section className="context-card work-brief-card">
      <header><div><span className="eyebrow">WORK BRIEF</span><h2>Current outcome</h2></div><StatusPill tone={percent === 100 ? 'success' : 'attention'}>{percent}%</StatusPill></header>
      {loading ? <StudioSkeleton /> : brief?.contract === undefined ? (
        <StudioEmpty title="No outcome contract" detail="Define the outcome and completion proof in the conversation." />
      ) : (
        <>
          <strong>{brief.contract.goal ?? 'Outcome not named'}</strong>
          <p>{brief.contract.deliverable ?? 'Deliverable not specified'}</p>
          <div className="verification-progress"><span style={{ width: `${Math.max(0, Math.min(100, percent))}%` }} /></div>
          {brief.blocking_reason === undefined || brief.blocking_reason === '' ? null : <p className="context-blocker"><b>Blocked:</b> {brief.blocking_reason}</p>}
          <footer><span>Next</span><strong>{brief.next_action ?? 'Continue the accepted plan'}</strong></footer>
        </>
      )}
    </section>
  )
}

function ActivityCard({ events }: { events: EventEnvelope[] }) {
  return (
    <section className="context-card activity-card">
      <header><div><span className="eyebrow">ACTIVITY</span><h2>Engineering work</h2></div><span className="live-indicator"><i /> Live</span></header>
      {events.length === 0 ? <p className="studio-muted">No recent structured engineering activity.</p> : (
        <ol>{events.slice(-6).reverse().map((event) => (
          <li key={event.event_id}>
            <span className={`activity-mark activity-${activityTone(event.type)}`}><Icon name={event.type.includes('failed') ? 'close' : event.type.includes('completed') ? 'check' : 'activity'} /></span>
            <div>
              <strong>{activityLabel(event.type)}</strong>
              <time dateTime={event.occurred_at}>{relativeTime(event.occurred_at)}</time>
              <details className="activity-details">
                <summary>Technical details</summary>
                <dl>
                  <div><dt>Event</dt><dd>{event.type}</dd></div>
                  <div><dt>Sequence</dt><dd>{event.sequence}</dd></div>
                  {activityPayloadDetails(event.payload).map(([label, value]) => (
                    <div key={label}><dt>{humanize(label)}</dt><dd>{value}</dd></div>
                  ))}
                </dl>
              </details>
            </div>
          </li>
        ))}</ol>
      )}
    </section>
  )
}

function PanelSection({
  aside,
  children,
  kicker,
  title,
}: {
  aside?: ReactNode
  children: ReactNode
  kicker: string
  title: string
}) {
  return (
    <section className="studio-section">
      <header>
        <div><span className="operation-kicker">{kicker}</span><h2>{title}</h2></div>
        {aside}
      </header>
      <div className="studio-section-content">{children}</div>
    </section>
  )
}

function StatusPill({
  children,
  tone,
}: {
  children: ReactNode
  tone: 'success' | 'attention' | 'danger' | 'quiet'
}) {
  return <span className={`studio-status tone-${tone}`}><i aria-hidden="true" />{children}</span>
}

function StudioEmpty({ title, detail }: { title: string; detail: string }) {
  return <div className="studio-empty"><strong>{title}</strong><p>{detail}</p></div>
}

function StudioSkeleton() {
  return <div aria-label="Loading" className="studio-skeleton"><span /><span /><span /></div>
}

function PlainList({ values, empty }: { values: string[]; empty: string }) {
  if (values.length === 0) return <p className="studio-muted">{empty}</p>
  return <ul className="plain-check-list">{values.map((value, index) => <li key={`${value}-${String(index)}`}><Icon name="check" />{value}</li>)}</ul>
}

function SecurityFact({ label, value, good }: { label: string; value: string; good: boolean }) {
  return <section className="security-fact" data-good={good}><span><Icon name={good ? 'check' : 'shield'} /></span><div><small>{label}</small><strong>{value}</strong></div></section>
}

async function query<T>(
  operator: ReturnType<typeof useOperator>,
  operation: Operation,
  payload: unknown,
  scope: { session_id?: string } = {},
): Promise<T> {
  const response = await operator.client.query<T>(operation, payload, scope)
  if (response.error !== undefined) throw new Error(response.error.message)
  if (response.result === undefined) throw new Error(`${operation} returned no result`)
  if (
    typeof response.result === 'object' &&
    response.result !== null &&
    !Array.isArray(response.result)
  ) {
    const projection = response.result as Record<string, unknown>
    if (projection.status === 'unavailable' || projection.status === 'not_available') {
      throw new Error(
        typeof projection.reason === 'string'
          ? projection.reason
          : `${operation} is not available for this project`,
      )
    }
  }
  return response.result
}

function sessionScope(sessionID: string | undefined): { session_id?: string } {
  return sessionID === undefined ? {} : { session_id: sessionID }
}

function humanize(value: string): string {
  return value.replaceAll(/[._-]+/g, ' ').replaceAll(/\b\w/g, (letter) => letter.toUpperCase())
}

function shortHash(value: string | undefined): string {
  return value === undefined || value === '' ? 'No commit' : value.slice(0, 8)
}

function relativeTime(value: string): string {
  const timestamp = new Date(value).getTime()
  if (!Number.isFinite(timestamp)) return 'Recently'
  const seconds = Math.round((timestamp - Date.now()) / 1000)
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
  if (Math.abs(seconds) < 60) return formatter.format(seconds, 'second')
  const minutes = Math.round(seconds / 60)
  if (Math.abs(minutes) < 60) return formatter.format(minutes, 'minute')
  const hours = Math.round(minutes / 60)
  if (Math.abs(hours) < 24) return formatter.format(hours, 'hour')
  return formatter.format(Math.round(hours / 24), 'day')
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : typeof error === 'string' ? error : 'Try again shortly.'
}

function engineeringActivity(events: EventEnvelope[]): EventEnvelope[] {
  return events.filter((event) =>
    event.type.startsWith('workspace.') ||
    event.type.startsWith('task.') ||
    event.type.startsWith('agent.') ||
    event.type.startsWith('tool.') ||
    event.type.startsWith('deployment.') ||
    event.type.startsWith('verification.') ||
    event.type === 'turn.recovery' || event.type === 'turn.incomplete',
  )
}

function activityPayloadDetails(payload: unknown): Array<[string, string]> {
  if (typeof payload !== 'object' || payload === null || Array.isArray(payload)) return []
  const values = payload as Record<string, unknown>
  const details: Array<[string, string]> = []
  for (const key of ['operation', 'name', 'phase', 'status', 'message', 'next_action']) {
    const value = values[key]
    if (typeof value === 'string' && value.trim() !== '') {
      details.push([key, value.length > 240 ? `${value.slice(0, 240)}…` : value])
    }
  }
  return details.slice(0, 4)
}

function activityTone(type: string): string {
  if (type.includes('failed') || type.includes('cancelled')) return 'danger'
  if (type.includes('completed')) return 'success'
  return 'active'
}

function activityLabel(type: string): string {
  const labels: Record<string, string> = {
    'workspace.operation.queued': 'Workspace action queued',
    'workspace.operation.started': 'Workspace action started',
    'workspace.operation.progress': 'Workspace action progressing',
    'workspace.operation.completed': 'Workspace action completed',
    'workspace.operation.failed': 'Workspace action failed',
    'workspace.operation.cancelled': 'Workspace action cancelled',
    'turn.recovery': 'Work restored after reconnect',
	'turn.incomplete': 'Work paused with progress saved',
  }
  return labels[type] ?? humanize(type)
}
