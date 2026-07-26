import {
  type ChangeEvent,
  type FormEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { Icon } from '../components/Icon'

interface OfficeDocument {
  id: string
  title: string
  kind: 'document' | 'spreadsheet' | 'presentation' | 'pdf'
  extension: string
  current_version_id: string
  starred: boolean
  archived_at?: string
  created_at: string
  updated_at: string
}

interface OfficeVersion {
  id: string
  sequence: number
  extension: string
  size_bytes: number
  origin: string
  created_at: string
}

interface OfficeStatus {
  configured: boolean
  available: boolean
  engine: string
  message: string
  version?: string
  public_path: string
}

interface OfficeSession {
  id: string
  document_id: string
  editor_config: Record<string, unknown>
  expires_at: string
}

interface DocumentEditor {
  destroyEditor?: () => void
}

interface DocsAPI {
  DocEditor: new (elementID: string, config: Record<string, unknown>) => DocumentEditor
}

declare global {
  interface Window {
    DocsAPI?: DocsAPI
  }
}

const API_BASE = '/v1/office'

export function OfficeStudio() {
  const [status, setStatus] = useState<OfficeStatus>()
  const [documents, setDocuments] = useState<OfficeDocument[]>([])
  const [activeDocument, setActiveDocument] = useState<OfficeDocument>()
  const [session, setSession] = useState<OfficeSession>()
  const [versions, setVersions] = useState<OfficeVersion[]>([])
  const [archived, setArchived] = useState(false)
  const [search, setSearch] = useState('')
  const [busy, setBusy] = useState(true)
  const [notice, setNotice] = useState('')
  const uploadRef = useRef<HTMLInputElement>(null)

  const loadDocuments = useCallback(async (showArchived: boolean) => {
    const found = await officeRequest<OfficeDocument[] | null>(
      `/documents?archived=${showArchived ? 'true' : 'false'}`,
    )
    setDocuments(found ?? [])
  }, [])

  useEffect(() => {
    void Promise.all([
      officeRequest<OfficeStatus>('/status').then(setStatus),
      loadDocuments(false),
    ]).catch((error: unknown) => {
      setNotice(errorMessage(error))
    }).finally(() => setBusy(false))
  }, [loadDocuments])

  const visibleDocuments = useMemo(() => {
    const query = search.trim().toLocaleLowerCase()
    return query === ''
      ? documents
      : documents.filter((document) => document.title.toLocaleLowerCase().includes(query))
  }, [documents, search])

  const createDocument = async (
    kind: OfficeDocument['kind'],
    title: string,
  ) => {
    setNotice('')
    try {
      const document = await officeMutation<OfficeDocument>('/documents', 'POST', {
        kind,
        title,
      })
      setDocuments((current) => [document, ...current])
      await openDocument(document)
    } catch (error) {
      setNotice(errorMessage(error))
    }
  }

  const uploadDocument = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (file === undefined) return
    const form = new FormData()
    form.set('file', file)
    form.set('title', file.name.replace(/\.[^.]+$/, ''))
    setNotice('Importing document…')
    try {
      const document = await officeMutation<OfficeDocument>(
        '/documents/upload',
        'POST',
        form,
      )
      setDocuments((current) => [document, ...current])
      setNotice('The imported file is stored as an encrypted immutable version.')
    } catch (error) {
      setNotice(errorMessage(error))
    }
  }

  const openDocument = async (document: OfficeDocument) => {
    setActiveDocument(document)
    setSession(undefined)
    setVersions([])
    setNotice('Opening editor…')
    try {
      const [opened, history] = await Promise.all([
        officeMutation<OfficeSession>(`/documents/${document.id}/session`, 'POST'),
        officeRequest<OfficeVersion[] | null>(`/documents/${document.id}/versions`),
      ])
      setSession(opened)
      setVersions(history ?? [])
      setNotice('')
    } catch (error) {
      setNotice(errorMessage(error))
    }
  }

  const updateDocument = async (
    document: OfficeDocument,
    update: Record<string, unknown>,
  ) => {
    await officeMutation<void>(`/documents/${document.id}`, 'PATCH', update)
    await loadDocuments(archived)
  }

  const renameDocument = async (document: OfficeDocument) => {
    const title = window.prompt('Document name', document.title)?.trim()
    if (title === undefined || title === '' || title === document.title) return
    try {
      await updateDocument(document, { title })
    } catch (error) {
      setNotice(errorMessage(error))
    }
  }

  const archiveDocument = async (document: OfficeDocument) => {
    try {
      await updateDocument(document, { archive: !archived })
    } catch (error) {
      setNotice(errorMessage(error))
    }
  }

  const deleteDocument = async (document: OfficeDocument) => {
    if (!window.confirm(`Delete “${document.title}”? Its versions will no longer be available.`)) {
      return
    }
    try {
      await officeMutation<void>(`/documents/${document.id}`, 'DELETE')
      await loadDocuments(archived)
    } catch (error) {
      setNotice(errorMessage(error))
    }
  }

  const restoreVersion = async (version: OfficeVersion) => {
    if (activeDocument === undefined) return
    setNotice(`Restoring version ${String(version.sequence)}…`)
    try {
      await officeMutation<OfficeVersion>(
        `/documents/${activeDocument.id}/versions/${version.id}/restore`,
        'POST',
      )
      const [document, history] = await Promise.all([
        officeRequest<OfficeDocument>(`/documents/${activeDocument.id}`),
        officeRequest<OfficeVersion[] | null>(`/documents/${activeDocument.id}/versions`),
      ])
      setActiveDocument(document)
      setVersions(history ?? [])
      setSession(undefined)
      setNotice('The historical content was restored as a new immutable version. Reopen the editor to continue.')
    } catch (error) {
      setNotice(errorMessage(error))
    }
  }

  const showArchived = async () => {
    const next = !archived
    setArchived(next)
    setBusy(true)
    try {
      await loadDocuments(next)
    } catch (error) {
      setNotice(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }

  if (activeDocument !== undefined) {
    return (
      <section className="office-editor-shell" aria-labelledby="office-document-title">
        <header className="office-editor-toolbar">
          <button
            className="office-back"
            onClick={() => {
              setActiveDocument(undefined)
              setSession(undefined)
              void loadDocuments(archived)
            }}
            type="button"
          >
            <Icon name="panel-left-open" />
            Library
          </button>
          <div>
            <h1 id="office-document-title">{activeDocument.title}</h1>
            <p>{activeDocument.extension.slice(1).toUpperCase()} · encrypted version history</p>
          </div>
          <a
            className="office-download"
            href={`${API_BASE}/documents/${activeDocument.id}/download`}
          >
            Download
          </a>
        </header>
        <p className="office-notice" hidden={notice === ''} role="status">
          {notice}
        </p>
        <div className="office-editor-layout">
          <div className="office-editor-host">
            {session === undefined ? (
              <div className="office-empty-state">
                <h2>Editor unavailable</h2>
                <p>{notice === '' ? 'The editor session is not connected.' : notice}</p>
                <button onClick={() => { void openDocument(activeDocument) }} type="button">
                  Try again
                </button>
              </div>
            ) : (
              <OnlyOfficeEditor
                config={session.editor_config}
                publicPath={status?.public_path ?? '/office-engine/'}
                sessionID={session.id}
                onError={(message) => setNotice(message)}
                onState={(message) => setNotice(message)}
              />
            )}
          </div>
          <aside className="office-version-rail" aria-label="Version history">
            <h2>Version history</h2>
            {versions.map((version) => (
              <div className="office-version" key={version.id}>
                <div>
                  <strong>Version {version.sequence}</strong>
                  <span>{formatDate(version.created_at)}</span>
                  <small>{humanOrigin(version.origin)} · {formatBytes(version.size_bytes)}</small>
                </div>
                {version.id !== activeDocument.current_version_id ? (
                  <button onClick={() => { void restoreVersion(version) }} type="button">
                    Restore
                  </button>
                ) : <span className="office-current-version">Current</span>}
              </div>
            ))}
          </aside>
        </div>
      </section>
    )
  }

  return (
    <section className="office-studio" aria-labelledby="office-studio-title">
      <header className="office-studio-header">
        <div>
          <p className="eyebrow">Productivity workspace</p>
          <h1 id="office-studio-title">Office Studio</h1>
          <p>Create, import, edit, and recover documents without leaving Ion.</p>
        </div>
        <div className="office-engine-state" data-ready={status?.available === true}>
          <span aria-hidden="true" />
          <div>
            <strong>{status?.engine ?? 'ONLYOFFICE'}</strong>
            <small>{status?.message ?? 'Checking engine connection'}</small>
          </div>
        </div>
      </header>

      {notice !== '' ? <p className="office-notice" role="status">{notice}</p> : null}

      {status?.configured !== true || status.available !== true ? (
        <div className="office-empty-state">
          <p className="eyebrow">Setup required</p>
          <h2>{status?.configured === true ? 'ONLYOFFICE is unavailable' : 'Connect a private ONLYOFFICE service'}</h2>
          <p>
            {status?.configured === true
              ? 'Ion cannot reach the configured editor engine. Existing encrypted versions remain safe; restore the engine connection to edit them.'
              : 'Add the Office deployment variables and shared JWT secret, then restart Ion. Documents remain unavailable until the engine passes its health check.'}
          </p>
        </div>
      ) : (
        <>
          <section className="office-create-panel" aria-labelledby="office-create-title">
            <div>
              <p className="eyebrow">Start fresh</p>
              <h2 id="office-create-title">New file</h2>
            </div>
            <div className="office-create-actions">
              <button onClick={() => { void createDocument('document', 'Untitled document') }} type="button">
                <Icon name="edit" />
                <span>Document<small>Write and format</small></span>
              </button>
              <button onClick={() => { void createDocument('spreadsheet', 'Untitled spreadsheet') }} type="button">
                <Icon name="activity" />
                <span>Spreadsheet<small>Calculate and organize</small></span>
              </button>
              <button onClick={() => { void createDocument('presentation', 'Untitled presentation') }} type="button">
                <Icon name="workflow" />
                <span>Presentation<small>Build a slide deck</small></span>
              </button>
              <button onClick={() => uploadRef.current?.click()} type="button">
                <Icon name="paperclip" />
                <span>Import file<small>DOCX, XLSX, PPTX, or PDF</small></span>
              </button>
              <input
                accept=".docx,.xlsx,.pptx,.pdf"
                className="sr-only"
                onChange={(event) => { void uploadDocument(event) }}
                ref={uploadRef}
                type="file"
              />
            </div>
          </section>

          <section className="office-library" aria-labelledby="office-library-title">
            <header>
              <div>
                <p className="eyebrow">Encrypted library</p>
                <h2 id="office-library-title">{archived ? 'Archived files' : 'Recent files'}</h2>
              </div>
              <div className="office-library-tools">
                <label>
                  <span className="sr-only">Search files</span>
                  <Icon name="search" />
                  <input
                    onChange={(event) => setSearch(event.target.value)}
                    placeholder="Search files"
                    type="search"
                    value={search}
                  />
                </label>
                <button onClick={() => { void showArchived() }} type="button">
                  <Icon name="archive" />
                  {archived ? 'Recent' : 'Archived'}
                </button>
              </div>
            </header>

            {busy ? <p className="office-library-message">Loading files…</p> : null}
            {!busy && visibleDocuments.length === 0 ? (
              <p className="office-library-message">
                {search === '' ? 'No files here yet.' : 'No files match this search.'}
              </p>
            ) : null}
            <div className="office-document-list">
              {visibleDocuments.map((document) => (
                <article className="office-document-row" key={document.id}>
                  <button
                    className="office-document-main"
                    onClick={() => { void openDocument(document) }}
                    type="button"
                  >
                    <span className="office-file-kind">{document.extension.slice(1)}</span>
                    <span>
                      <strong>{document.title}</strong>
                      <small>Updated {formatDate(document.updated_at)}</small>
                    </span>
                  </button>
                  <div className="office-document-actions">
                    {!archived ? (
                      <button
                        aria-label={document.starred ? `Unstar ${document.title}` : `Star ${document.title}`}
                        onClick={() => { void updateDocument(document, { star: !document.starred }) }}
                        type="button"
                      >
                        {document.starred ? 'Starred' : 'Star'}
                      </button>
                    ) : null}
                    <button onClick={() => { void renameDocument(document) }} type="button">Rename</button>
                    <button onClick={() => { void archiveDocument(document) }} type="button">
                      {archived ? 'Restore' : 'Archive'}
                    </button>
                    <button onClick={() => { void deleteDocument(document) }} type="button">Delete</button>
                  </div>
                </article>
              ))}
            </div>
          </section>
        </>
      )}
    </section>
  )
}

function OnlyOfficeEditor({
  config,
  publicPath,
  sessionID,
  onError,
  onState,
}: {
  config: Record<string, unknown>
  publicPath: string
  sessionID: string
  onError: (message: string) => void
  onState: (message: string) => void
}) {
  const hostID = useMemo(() => `office-editor-${crypto.randomUUID()}`, [])
  const onErrorRef = useRef(onError)
  const onStateRef = useRef(onState)
  onErrorRef.current = onError
  onStateRef.current = onState

  useEffect(() => {
    let editor: DocumentEditor | undefined
    let cancelled = false
    let documentReady = false
    const startedAt = performance.now()
    const report = (event: OfficeClientEvent, errorCode?: number) => {
      void reportOfficeLifecycle(sessionID, event, performance.now() - startedAt, errorCode)
    }
    report('mount_started')
    const mount = async () => {
      try {
        await clearOfficeServiceWorkers(publicPath)
      } catch {
        if (!cancelled) {
          report('cache_reset_failed')
          onErrorRef.current('The Office editor cache could not be reset. Reload Ion and try again.')
        }
        return
      }
      if (cancelled || window.DocsAPI === undefined) return
      const events = {
        onAppReady: () => {
          report('app_ready')
          onStateRef.current('Office editor connected. Loading document…')
        },
        onDocumentReady: () => {
          documentReady = true
          report('document_ready')
          onStateRef.current('')
        },
        onDocumentStateChange: (event: { data?: boolean }) => {
          onStateRef.current(event.data === true ? 'Saving changes…' : 'All changes saved')
        },
        onError: (event: { data?: { errorCode?: number } }) => {
          report('editor_error', event.data?.errorCode)
          onErrorRef.current('ONLYOFFICE reported an editor error. Your last committed version is still available.')
        },
        onOutdatedVersion: () => {
          report('outdated_version')
          onErrorRef.current('A newer document version is available. Return to the library and reopen this file.')
        },
      }
      report('constructor_started')
      const originalSetAttribute = HTMLIFrameElement.prototype.setAttribute
      HTMLIFrameElement.prototype.setAttribute = function (
        qualifiedName: string,
        value: string,
      ) {
        return originalSetAttribute.call(
          this,
          qualifiedName,
          qualifiedName.toLocaleLowerCase() === 'allow'
            ? permissionsWithUnload(value)
            : value,
        )
      }
      try {
        editor = new window.DocsAPI.DocEditor(hostID, { ...config, events })
        report('constructor_returned')
      } finally {
        HTMLIFrameElement.prototype.setAttribute = originalSetAttribute
      }
      const frame = document.getElementById(hostID)
      if (frame instanceof HTMLIFrameElement) grantUnloadPermission(frame)
    }
    if (window.DocsAPI !== undefined) {
      report('api_loaded')
      void mount()
    } else {
      const source = `${publicPath.replace(/\/?$/, '/')}web-apps/apps/api/documents/api.js`
      const existing = document.querySelector<HTMLScriptElement>(`script[data-office-api="${source}"]`)
      const script = existing ?? document.createElement('script')
      const loaded = () => {
        report('api_loaded')
        void mount()
      }
      const failed = () => {
        report('api_load_failed')
        onErrorRef.current('The ONLYOFFICE editor client could not be loaded.')
      }
      script.addEventListener('load', loaded)
      script.addEventListener('error', failed)
      if (existing === null) {
        script.src = source
        script.async = true
        script.dataset.officeApi = source
        document.head.appendChild(script)
      }
      return () => {
        cancelled = true
        script.removeEventListener('load', loaded)
        script.removeEventListener('error', failed)
        report(documentReady ? 'cleanup_after_ready' : 'cleanup_before_ready')
        editor?.destroyEditor?.()
      }
    }
    return () => {
      cancelled = true
      report(documentReady ? 'cleanup_after_ready' : 'cleanup_before_ready')
      editor?.destroyEditor?.()
    }
  }, [config, hostID, publicPath, sessionID])

  return <div className="office-editor-frame" id={hostID} />
}

type OfficeClientEvent =
  | 'mount_started'
  | 'api_loaded'
  | 'constructor_started'
  | 'constructor_returned'
  | 'app_ready'
  | 'document_ready'
  | 'editor_error'
  | 'outdated_version'
  | 'cleanup_before_ready'
  | 'cleanup_after_ready'
  | 'api_load_failed'
  | 'cache_reset_failed'

async function reportOfficeLifecycle(
  sessionID: string,
  event: OfficeClientEvent,
  elapsedMS: number,
  errorCode?: number,
) {
  const body: Record<string, unknown> = {
    event,
    elapsed_ms: Math.max(0, Math.round(elapsedMS)),
  }
  if (errorCode !== undefined && Number.isFinite(errorCode)) body.error_code = errorCode
  await fetch(`${API_BASE}/sessions/${encodeURIComponent(sessionID)}/events`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'X-Ion-CSRF': readCookie('__Host-ion_csrf'),
    },
    body: JSON.stringify(body),
    keepalive: true,
  }).catch(() => undefined)
}

function grantUnloadPermission(frame: HTMLIFrameElement) {
  frame.allow = permissionsWithUnload(frame.allow)
}

function permissionsWithUnload(value: string) {
  const permissions = new Set(
    value
      .split(';')
      .map((permission) => permission.trim())
      .filter((permission) => permission !== ''),
  )
  permissions.add('unload')
  return [...permissions].join('; ')
}

async function clearOfficeServiceWorkers(publicPath: string) {
  if (!('serviceWorker' in navigator)) return
  const officePath = new URL(publicPath, window.location.href).pathname
  const registrations = await navigator.serviceWorker.getRegistrations()
  await Promise.all(
    registrations
      .filter((registration) => new URL(registration.scope).pathname.startsWith(officePath))
      .map((registration) => registration.unregister()),
  )
  if (!('caches' in window)) return
  const cacheNames = await window.caches.keys()
  await Promise.all(
    cacheNames
      .filter((cacheName) => cacheName.startsWith('document_editor_static_'))
      .map((cacheName) => window.caches.delete(cacheName)),
  )
}

async function officeRequest<T>(path: string): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  })
  return decodeOfficeResponse<T>(response)
}

async function officeMutation<T>(
  path: string,
  method: 'POST' | 'PATCH' | 'DELETE',
  input?: Record<string, unknown> | FormData,
): Promise<T> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    'X-Ion-CSRF': readCookie('__Host-ion_csrf'),
  }
  let body: BodyInit | undefined
  if (input instanceof FormData) {
    body = input
  } else if (input !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(input)
  }
  const response = await fetch(`${API_BASE}${path}`, {
    method,
    credentials: 'same-origin',
    headers,
    ...(body === undefined ? {} : { body }),
  })
  return decodeOfficeResponse<T>(response)
}

async function decodeOfficeResponse<T>(response: Response): Promise<T> {
  if (response.ok) {
    if (response.status === 204) return undefined as T
    return await response.json() as T
  }
  const payload = await response.json().catch(() => ({ error: 'The Office request failed.' })) as {
    error?: string
  }
  throw new Error(payload.error ?? 'The Office request failed.')
}

function readCookie(name: string): string {
  const prefix = `${name}=`
  for (const value of document.cookie.split(';')) {
    const trimmed = value.trim()
    if (trimmed.startsWith(prefix)) return decodeURIComponent(trimmed.slice(prefix.length))
  }
  return ''
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

function formatBytes(value: number): string {
  if (value < 1024) return `${String(value)} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

function humanOrigin(origin: string): string {
  return origin.replaceAll('_', ' ')
}
