import type {
  ComputerSourceReference,
  DisplayBlock,
  DisplayDatum,
  DisplayField,
  DisplayItem,
  DisplayKind,
  DisplayModel,
  EventEnvelope,
} from '@matrixmcl/ion-shared'

interface ApplicationProps {
  display: DisplayModel
  event: EventEnvelope
  migrated: boolean
  sources: ComputerSourceReference[]
}

type ApplicationRenderer = (props: ApplicationProps) => React.ReactNode

const renderers: Partial<Record<DisplayKind, ApplicationRenderer>> = {
  search: ResearchApplication,
  reader: ResearchApplication,
  navigation: WorkspaceApplication,
  repository: WorkspaceApplication,
  code: WorkspaceApplication,
  diff: WorkspaceApplication,
  terminal: TerminalApplication,
  process: TerminalApplication,
  table: DeliverableApplication,
  chart: DeliverableApplication,
  document: DeliverableApplication,
  artifact: DeliverableApplication,
  task: TaskApplication,
  agent: TaskApplication,
  approval: TaskApplication,
  error: GenericApplication,
  degraded: GenericApplication,
}

export function NativeApplication(props: ApplicationProps) {
  const Renderer = renderers[props.display.kind] ?? GenericApplication
  return <Renderer {...props} />
}

function ResearchApplication(props: ApplicationProps) {
  const { display } = props
  const query = field(display.fields, 'Query')
  const target = field(display.fields, 'URL')
  return (
    <ApplicationFrame {...props} family="Research" renderer="research">
      {query === undefined && target === undefined ? null : (
        <div className="computer-app-location">
          {query === undefined ? null : (
            <div>
              <span>Search query</span>
              <strong>{query.value}</strong>
              <DatumMeta datum={query} />
            </div>
          )}
          {target === undefined ? null : (
            <div>
              <span>Opened address</span>
              <SafeLink datum={target} />
              <DatumMeta datum={target} />
            </div>
          )}
        </div>
      )}
      {display.fields === undefined ? null : (
        <FieldGrid fields={display.fields.filter(
          (item) => !['Query', 'URL'].includes(item.label),
        )} />
      )}
      {display.blocks?.map((block, index) => (
        block.kind === 'list' && display.kind === 'search'
          ? <SearchResults block={block} key={blockKey(block, index)} />
          : <NativeBlock block={block} key={blockKey(block, index)} />
      ))}
    </ApplicationFrame>
  )
}

function WorkspaceApplication(props: ApplicationProps) {
  const { display } = props
  const path = field(display.fields, 'Path')
  const revision = field(display.fields, 'Revision')
  const branch = field(display.fields, 'Branch')
  return (
    <ApplicationFrame {...props} family="Repository & code" renderer="workspace">
      <div className="computer-workspace-meta">
        <FieldCard label="Workspace path" value={path} />
        <FieldCard label="Revision" value={revision} />
        <FieldCard label="Branch" value={branch} />
      </div>
      {display.fields === undefined ? null : (
        <FieldGrid fields={display.fields.filter(
          (item) => !['Path', 'Revision', 'Branch'].includes(item.label),
        )} />
      )}
      {display.blocks?.map((block, index) => (
        block.kind === 'list'
          ? <NavigationTree block={block} key={blockKey(block, index)} />
          : <NativeBlock block={block} key={blockKey(block, index)} />
      ))}
    </ApplicationFrame>
  )
}

function TerminalApplication(props: ApplicationProps) {
  const { display } = props
  const command = field(display.fields, 'Command')
  const exit = field(display.fields, 'Exit code')
  const duration = field(display.fields, 'Duration')
  return (
    <ApplicationFrame {...props} family="Terminal & process" renderer="terminal">
      <div className="computer-terminal-status">
        <FieldCard label="Working directory" value={field(display.fields, 'Working directory')} />
        <FieldCard label="Exit status" value={exit} />
        <FieldCard label="Duration" value={duration} />
        <FieldCard label="Process state" value={field(display.fields, 'Status')} />
      </div>
      {command === undefined ? null : (
        <div className="computer-command">
          <span>Redacted command</span>
          <code>{command.value}</code>
          <DatumMeta datum={command} />
        </div>
      )}
      {display.blocks?.map((block, index) => (
        <NativeBlock block={block} key={blockKey(block, index)} />
      ))}
      {display.fields === undefined ? null : (
        <FieldGrid fields={display.fields.filter(
          (item) => !['Command', 'Exit code', 'Duration', 'Working directory', 'Status'].includes(item.label),
        )} />
      )}
    </ApplicationFrame>
  )
}

function DeliverableApplication(props: ApplicationProps) {
  const { display } = props
  return (
    <ApplicationFrame {...props} family="Document & data" renderer="deliverable">
      <FieldGrid fields={display.fields ?? []} />
      {display.blocks?.map((block, index) => (
        <NativeBlock block={block} key={blockKey(block, index)} />
      ))}
      {display.blocks?.length === undefined || display.blocks.length === 0 ? (
        <div className="computer-app-empty">
          The validated artifact has metadata but no inline preview.
        </div>
      ) : null}
    </ApplicationFrame>
  )
}

function TaskApplication(props: ApplicationProps) {
  const { display } = props
  const status = field(display.fields, 'Status')
  return (
    <ApplicationFrame {...props} family="Task & agent" renderer="task">
      <div className="computer-task-summary">
        <FieldCard label="Assignment" value={field(display.fields, 'Assignment')} />
        <FieldCard label="Status" value={status} />
        <FieldCard label="Budget" value={field(display.fields, 'Budget')} />
        <FieldCard label="Coverage" value={field(display.fields, 'Coverage')} />
      </div>
      <FieldGrid fields={(display.fields ?? []).filter(
        (item) => !['Assignment', 'Status', 'Budget', 'Coverage'].includes(item.label),
      )} />
      {display.blocks?.map((block, index) => (
        <NativeBlock block={block} key={blockKey(block, index)} />
      ))}
    </ApplicationFrame>
  )
}

function GenericApplication(props: ApplicationProps) {
  return (
    <ApplicationFrame {...props} family="Evidence" renderer="generic">
      <FieldGrid fields={props.display.fields ?? []} />
      {props.display.blocks?.map((block, index) => (
        <NativeBlock block={block} key={blockKey(block, index)} />
      ))}
    </ApplicationFrame>
  )
}

function ApplicationFrame({
  children,
  display,
  event,
  family,
  migrated,
  renderer,
  sources,
}: ApplicationProps & {
  children: React.ReactNode
  family: string
  renderer: string
}) {
  const captured = sources.filter((source) =>
    source.kind === 'screenshot' || source.kind === 'visual_snapshot'
  )
  return (
    <article
      className="computer-display computer-native-app"
      data-kind={display.kind}
      data-renderer={renderer}
    >
      <header>
        <div>
          <p>{family}</p>
          <h3>{display.title.value}</h3>
        </div>
        <div className="computer-app-badges">
          <DatumMeta datum={display.title} />
          {migrated ? <span>Compatible retained view</span> : null}
        </div>
      </header>
      {children}
      {captured.length === 0 ? null : (
        <section className="computer-captured-evidence">
          <h4>Captured visual evidence</h4>
          <p>
            A sanitized capture is referenced for appearance review. Semantic data remains the
            primary view.
          </p>
          <SourceList sources={captured} />
        </section>
      )}
      <footer>
        <span>Event {String(event.sequence)} · {formatTimestamp(event.occurred_at)}</span>
        <SourceList sources={sources} />
      </footer>
    </article>
  )
}

function SearchResults({ block }: { block: DisplayBlock }) {
  return (
    <section className="computer-search-results">
      <h4>{block.label ?? 'Results'}</h4>
      {block.items?.length === 0 ? <p>No validated results were available.</p> : null}
      <ol>
        {block.items?.map((item, index) => {
          const title = itemField(item, 'Title') ?? itemField(item, 'Name') ??
            itemField(item, 'Path')
          const target = itemField(item, 'URL')
          const snippet = itemField(item, 'Snippet') ?? itemField(item, 'Text')
          const metadata = item.fields.filter(
            (field) => !['Title', 'Name', 'Path', 'URL', 'Snippet', 'Text'].includes(field.label),
          )
          return (
            <li key={String(index)}>
              <span>Result {String(index + 1)}</span>
              {target === undefined ? (
                <strong>{title?.value ?? 'Untitled result'}</strong>
              ) : (
                <SafeLink datum={{
                  ...target,
                  value: target.value,
                }} label={title?.value ?? target.value} />
              )}
              {snippet === undefined ? null : <p>{snippet.value}</p>}
              <div className="computer-search-source">
                {metadata.map((field) => (
                  <span key={field.label}>
                    <strong>{field.label}</strong> {field.value.value}
                  </span>
                ))}
                {title === undefined ? null : <DatumMeta datum={title} />}
                {target === undefined ? null : <DatumMeta datum={target} />}
              </div>
            </li>
          )
        })}
      </ol>
    </section>
  )
}

function NavigationTree({ block }: { block: DisplayBlock }) {
  return (
    <section className="computer-navigation-tree">
      <h4>{block.label ?? 'Files'}</h4>
      <ul>
        {block.items?.map((item, index) => {
          const name = itemField(item, 'Name') ?? itemField(item, 'Path')
          const kind = itemField(item, 'Type')
          return (
            <li key={String(index)}>
              <span>{kind?.value === 'directory' ? 'Folder' : 'File'}</span>
              <strong>{name?.value ?? 'Unnamed entry'}</strong>
              {kind === undefined ? null : <DatumMeta datum={kind} />}
            </li>
          )
        })}
      </ul>
    </section>
  )
}

function NativeBlock({ block }: { block: DisplayBlock }) {
  if (block.kind === 'list') {
    return (
      <section className="computer-native-block" data-block={block.kind}>
        <h4>{block.label ?? 'Items'}</h4>
        <ul>
          {block.items?.map((item, index) => (
            <li key={String(index)}>
              {item.fields.map((itemValue) => (
                <span key={itemValue.label}>
                  <strong>{itemValue.label}</strong> {itemValue.value.value}
                </span>
              ))}
            </li>
          ))}
        </ul>
      </section>
    )
  }
  if (block.kind === 'table' || block.kind === 'chart') {
    return <DataBlock block={block} />
  }
  const preformatted =
    block.kind === 'code' || block.kind === 'terminal' || block.kind === 'diff'
  return (
    <section className="computer-native-block" data-block={block.kind}>
      {block.label === undefined ? null : <h4>{block.label}</h4>}
      {block.content === undefined ? null : preformatted ? (
        <pre
          aria-label={`${humanize(block.kind)} output`}
          role={block.kind === 'terminal' ? 'log' : undefined}
        >
          <code>{block.content.value}</code>
        </pre>
      ) : (
        <div className="computer-document-content">
          <p>{block.content.value}</p>
        </div>
      )}
      {block.content === undefined ? null : <DatumMeta datum={block.content} />}
      <FieldGrid fields={block.fields ?? []} />
    </section>
  )
}

function DataBlock({ block }: { block: DisplayBlock }) {
  return (
    <section className="computer-native-block computer-data-block" data-block={block.kind}>
      <h4>{block.label ?? humanize(block.kind)}</h4>
      {block.kind === 'chart' ? (
        <p className="computer-chart-description">
          Validated chart data. Values remain available in the accessible table.
        </p>
      ) : null}
      <div className="computer-table-scroll">
        <table>
          <caption>{block.label ?? `${humanize(block.kind)} data`}</caption>
          {block.columns === undefined ? null : (
            <thead>
              <tr>{block.columns.map((column) => <th key={column} scope="col">{column}</th>)}</tr>
            </thead>
          )}
          <tbody>
            {block.rows?.map((row, rowIndex) => (
              <tr key={String(rowIndex)}>
                {row.map((datum, cellIndex) => (
                  <td key={String(cellIndex)}>
                    {datum.value}
                    <DatumMeta datum={datum} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function FieldGrid({ fields }: { fields: DisplayField[] }) {
  if (fields.length === 0) return null
  return (
    <dl className="computer-display-fields">
      {fields.map((item) => (
        <div key={item.label}>
          <dt>{item.label}</dt>
          <dd>
            {item.value.format === 'url'
              ? <SafeLink datum={item.value} />
              : item.value.value}
          </dd>
          <DatumMeta datum={item.value} />
        </div>
      ))}
    </dl>
  )
}

function FieldCard({ label, value }: { label: string; value: DisplayDatum | undefined }) {
  return (
    <div>
      <span>{label}</span>
      <strong>{value?.value ?? 'Unavailable'}</strong>
      {value === undefined ? null : <DatumMeta datum={value} />}
    </div>
  )
}

function DatumMeta({ datum }: { datum: DisplayDatum }) {
  return (
    <span className="computer-datum-meta">
      {humanize(datum.truth)} · {datum.sources.map((source) => `Source ${String(source + 1)}`).join(', ')}
    </span>
  )
}

function SourceList({ sources }: { sources: ComputerSourceReference[] }) {
  return (
    <ul className="computer-source-list" aria-label="Application sources">
      {sources.map((source, index) => (
        <li key={`${source.kind}-${source.id}`}>
          <span>{humanize(source.kind)}</span>
          <code title={source.id}>Source {String(index + 1)}</code>
        </li>
      ))}
    </ul>
  )
}

function SafeLink({ datum, label }: { datum: DisplayDatum; label?: string }) {
  const safe = safeURL(datum.value)
  return safe === undefined
    ? <span>{label ?? datum.value}</span>
    : (
        <a href={safe} rel="noreferrer noopener nofollow" target="_blank">
          {label ?? datum.value}
        </a>
      )
}

function safeURL(value: string): string | undefined {
  try {
    const parsed = new URL(value)
    return parsed.protocol === 'https:' || parsed.protocol === 'http:'
      ? parsed.toString()
      : undefined
  } catch {
    return undefined
  }
}

function field(fields: DisplayField[] | undefined, label: string) {
  return fields?.find((item) => item.label === label)?.value
}

function itemField(item: DisplayItem, label: string) {
  return item.fields.find((candidate) => candidate.label === label)?.value
}

function blockKey(block: DisplayBlock, index: number) {
  return `${block.kind}-${String(index)}`
}

function humanize(value: string) {
  return value.replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase())
}

function formatTimestamp(value: string) {
  const parsed = new Date(value)
  return Number.isNaN(parsed.valueOf())
    ? value
    : parsed.toLocaleString([], {
        dateStyle: 'medium',
        timeStyle: 'medium',
      })
}
