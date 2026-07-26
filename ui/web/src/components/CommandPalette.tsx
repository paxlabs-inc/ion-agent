import * as Dialog from '@radix-ui/react-dialog'
import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

const globalCommands = [
  { label: 'Start a conversation', path: '/chat' },
  { label: 'Open system overview', path: '/overview' },
  { label: 'Manage conversations', path: '/sessions' },
  { label: 'Review saved knowledge', path: '/knowledge' },
  { label: 'Review goals and active work', path: '/work' },
  { label: 'Open Software Studio', path: '/studio' },
  { label: 'Review actions and decisions', path: '/execution' },
  { label: 'Manage models and connections', path: '/extensions' },
  { label: 'Manage availability and schedules', path: '/presence' },
  { label: 'Review preferences and identity', path: '/identity' },
  { label: 'Review safety decisions', path: '/security' },
  { label: 'Check information integrity', path: '/integrity' },
  { label: 'Check system health', path: '/diagnostics' },
]

export function CommandPalette() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const navigate = useNavigate()
  const location = useLocation()
  const studioMatch = /^\/studio\/([0-9a-f-]+)$/.exec(location.pathname)
  const commands = studioMatch === null ? globalCommands : [
    ...[
      ['Plan', 'plan'],
      ['Changes', 'changes'],
      ['Code', 'code'],
      ['Terminal', 'terminal'],
      ['Preview', 'preview'],
      ['Problems', 'problems'],
      ['Tests', 'tests'],
      ['Security', 'security'],
      ['Data', 'data'],
      ['Deploy', 'deploy'],
    ].map(([label, panel]) => ({
      label: `Open project ${label}`,
      path: panel === 'plan'
        ? `/studio/${studioMatch[1]}`
        : `/studio/${studioMatch[1]}?panel=${panel}`,
    })),
    ...globalCommands,
  ]
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setOpen((value) => !value)
      }
    }
    const open = () => setOpen(true)
    window.addEventListener('keydown', listener)
    window.addEventListener('ion:open-command', open)
    return () => {
      window.removeEventListener('keydown', listener)
      window.removeEventListener('ion:open-command', open)
    }
  }, [])
  const filtered = commands.filter((command) =>
    command.label.toLowerCase().includes(query.toLowerCase()),
  )
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="command-dialog" aria-describedby={undefined}>
          <Dialog.Title>Search Ion</Dialog.Title>
          <input
            autoFocus
            aria-label="Filter commands"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Find a page or workflow…"
            value={query}
          />
          <div className="command-list">
            {filtered.map((command) => (
              <button
                key={command.path}
                onClick={() => {
                  navigate(command.path)
                  setOpen(false)
                  setQuery('')
                }}
                type="button"
              >
                {command.label}
              </button>
            ))}
          </div>
          <Dialog.Close asChild>
            <button className="quiet-button" type="button">
              Close
            </button>
          </Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
