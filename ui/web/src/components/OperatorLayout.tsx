import { useQuery } from '@tanstack/react-query'
import { type ReactNode, useEffect, useState } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import ionLogoForDarkSurface from '../../../../assets/ion-logo-light/Wide620x300Logo.png'
import ionLogoForLightSurface from '../../../../assets/ion-logo-dark/Wide620x300Logo.png'
import { useOperator, useOperatorState } from '../app/operator-context'
import { Icon, type IconName } from './Icon'

interface SessionSummary {
  id: string
  title: string
  preview?: string
  updated_at: string
}

const conversationNavigation: ReadonlyArray<[string, string, IconName]> = [
  ['/chat', 'Chat', 'spark'],
  ['/sessions', 'Conversations', 'history'],
]

const workspaceNavigation: ReadonlyArray<[string, string, IconName]> = [
  ['/computer', 'Computer', 'activity'],
  ['/media', 'Media Studio', 'spark'],
  ['/office', 'Office Studio', 'edit'],
  ['/studio', 'Software Studio', 'workflow'],
  ['/work', 'Projects & tasks', 'folder'],
  ['/knowledge', 'Knowledge', 'brain'],
]

const mainNavigation = [...conversationNavigation, ...workspaceNavigation] as const

const controlNavigation: ReadonlyArray<[string, string, IconName]> = [
  ['/overview', 'Overview', 'home'],
  ['/extensions', 'Models & connections', 'workflow'],
  ['/execution', 'Actions & decisions', 'activity'],
  ['/presence', 'Schedules & availability', 'history'],
  ['/identity', 'Personalization', 'spark'],
  ['/security', 'Safety', 'shield'],
  ['/integrity', 'Trust & sources', 'archive'],
  ['/diagnostics', 'System', 'settings'],
]

const pageTitles: Record<string, string> = {
  '/chat': 'Ion',
  '/sessions': 'Conversations',
  '/work': 'Projects & tasks',
  '/projects': 'Software Studio',
  '/studio': 'Software Studio',
  '/knowledge': 'Knowledge',
  '/computer': 'Computer',
  '/media': 'Image & Video Studio',
  '/office': 'Office Studio',
  '/extensions': 'Models & connections',
  '/execution': 'Actions & decisions',
  '/presence': 'Schedules & availability',
  '/identity': 'Personalization',
  '/security': 'Safety',
  '/integrity': 'Trust & sources',
  '/diagnostics': 'System',
  '/overview': 'Overview',
}

export function OperatorLayout({ children }: { children: ReactNode }) {
  const operator = useOperator()
  const { connection } = operator
  const state = useOperatorState()
  const location = useLocation()
  const navigate = useNavigate()
  const [mobileOpen, setMobileOpen] = useState(false)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [controlsOpen, setControlsOpen] = useState(
    controlNavigation.some(([path]) => path === location.pathname),
  )
  const [signingOut, setSigningOut] = useState(false)
  const [signOutFailed, setSignOutFailed] = useState(false)
  const authStatus = useQuery({
    queryKey: ['auth.status'],
    queryFn: async () => {
      const response = await fetch('/v1/auth/status', {
        credentials: 'same-origin',
        headers: { Accept: 'application/json' },
      })
      if (!response.ok) throw new Error('Authentication status unavailable')
      return await response.json() as { required: boolean; authenticated: boolean }
    },
    retry: false,
    staleTime: 60_000,
  })
  const sessions = useQuery({
    queryKey: ['session.list'],
    queryFn: async () => {
      const response = await operator.client.query<SessionSummary[]>('session.list', {})
      if (response.error !== undefined) throw new Error(response.error.message)
      return response.result ?? []
    },
    retry: false,
  })

  useEffect(() => {
    setMobileOpen(false)
    if (controlNavigation.some(([path]) => path === location.pathname)) {
      setControlsOpen(true)
    }
  }, [location.pathname])

  const newConversation = () => {
    operator.setSessionID(undefined)
    navigate('/chat')
  }
  const openConversation = (sessionID: string) => {
    operator.setSessionID(sessionID)
    navigate('/chat')
  }
  const openSearch = () => {
    window.dispatchEvent(new Event('ion:open-command'))
  }
  const signOut = async () => {
    if (signingOut) return
    setSigningOut(true)
    setSignOutFailed(false)
    try {
      const response = await fetch('/v1/auth/logout', {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          Accept: 'application/json',
          'X-Ion-CSRF': readCookie('__Host-ion_csrf'),
        },
      })
      if (!response.ok && response.status !== 401) {
        setSignOutFailed(true)
        return
      }
      window.location.assign('/')
    } catch {
      setSignOutFailed(true)
    } finally {
      setSigningOut(false)
    }
  }
  const connectionLabel =
    connection === 'ready' ? 'Ready' : connection === 'connecting' ? 'Connecting' : 'Offline'
  const pageTitle = location.pathname.startsWith('/studio/')
    ? 'Software Studio'
    : (pageTitles[location.pathname] ?? 'Ion')

  return (
    <div
      className="operator-shell"
      data-menu-open={mobileOpen}
      data-sidebar-collapsed={sidebarCollapsed}
    >
      <a className="skip-link" href="#main-content">Skip to content</a>
      <button
        aria-label="Close navigation"
        className="mobile-scrim"
        onClick={() => setMobileOpen(false)}
        type="button"
      />
      <aside className="shell-sidebar">
        <button
          aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-pressed={sidebarCollapsed}
          className="sidebar-collapse"
          onClick={() => setSidebarCollapsed((value) => !value)}
          title={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          type="button"
        >
          <Icon name={sidebarCollapsed ? 'panel-left-open' : 'panel-left-close'} />
        </button>
        <div className="sidebar-brand">
          <Link aria-label="Ion chat" className="brand-lockup" to="/chat">
            <picture className="brand-logo">
              <source media="(prefers-color-scheme: dark)" srcSet={ionLogoForDarkSurface} />
              <img alt="" src={ionLogoForLightSurface} />
            </picture>
          </Link>
          <button
            aria-label="Close navigation"
            className="icon-button sidebar-close"
            onClick={() => setMobileOpen(false)}
            type="button"
          >
            <Icon name="close" />
          </button>
        </div>

        <button className="new-chat-button" onClick={newConversation} title="New task" type="button">
          <Icon name="plus" />
          <span>New task</span>
        </button>
        <button className="sidebar-search" onClick={openSearch} title="Search" type="button">
          <Icon name="search" />
          <span>Search</span>
          <kbd>⌘ K</kbd>
        </button>

        <nav className="primary-nav" aria-label="Primary navigation">
          <div className="sidebar-nav-group">
            {conversationNavigation.map(([to, label, icon]) => (
              <NavLink key={to} title={label} to={to}>
                <Icon name={icon} />
                <span>{label}</span>
              </NavLink>
            ))}
          </div>

          <div className="sidebar-nav-group">
            <span className="sidebar-nav-label">Workspace</span>
            {workspaceNavigation.map(([to, label, icon]) => (
              <NavLink key={to} title={label} to={to}>
                <Icon name={icon} />
                <span>{label}</span>
              </NavLink>
            ))}
          </div>

          {sessions.data !== undefined && sessions.data.length > 0 ? (
            <section className="recent-chats" aria-labelledby="recent-chats-title">
              <div className="sidebar-section-heading">
                <span id="recent-chats-title">Recent</span>
                <Link to="/sessions">See all</Link>
              </div>
              {sessions.data.slice(0, 5).map((session) => (
                <button
                  className={operator.sessionID === session.id ? 'selected' : ''}
                  key={session.id}
                  onClick={() => openConversation(session.id)}
                  title={session.preview ?? session.title}
                  type="button"
                >
                  {session.title}
                </button>
              ))}
            </section>
          ) : null}

          <div className="sidebar-spacer" />
          <button
            aria-label="Settings & control"
            aria-expanded={controlsOpen}
            className="control-toggle"
            onClick={() => {
              if (sidebarCollapsed) {
                setSidebarCollapsed(false)
                setControlsOpen(true)
                return
              }
              setControlsOpen((value) => !value)
            }}
            title="Manage"
            type="button"
          >
            <Icon name="settings" />
            <span>Manage</span>
            <Icon className="toggle-chevron" name="chevron-down" />
          </button>
          {controlsOpen ? (
            <div className="control-navigation">
              {controlNavigation.map(([to, label, icon]) => (
                <NavLink key={to} title={label} to={to}>
                  <Icon name={icon} />
                  <span>{label}</span>
                </NavLink>
              ))}
            </div>
          ) : null}
        </nav>

        <div className="sidebar-foot">
          <span className={`connection-dot connection-${connection}`} aria-hidden="true" />
          <div>
            <strong>Local workspace</strong>
            <span>
              {connection === 'degraded'
                ? 'Reconnecting'
                : connection === 'connecting'
                  ? 'Connecting securely'
                  : `${connectionLabel} · Protected`}
            </span>
          </div>
          {authStatus.data?.required === true ? (
            <>
              <button
                className="sidebar-sign-out"
                disabled={signingOut}
                onClick={() => { void signOut() }}
                title={signOutFailed ? 'Sign out failed. Try again.' : 'Sign out'}
                type="button"
              >
                {signingOut ? 'Signing out…' : signOutFailed ? 'Try again' : 'Sign out'}
              </button>
              <span className="sr-only" role="status">
                {signOutFailed ? 'Sign out failed. Try again.' : ''}
              </span>
            </>
          ) : null}
        </div>
      </aside>

      <div className="shell-main">
        <header className="topbar">
          <button
            aria-label="Open navigation"
            className="icon-button mobile-menu-button"
            onClick={() => setMobileOpen(true)}
            type="button"
          >
            <Icon name="menu" />
          </button>
          <div className="topbar-heading">
            <span className="topbar-title">
              {pageTitle}
            </span>
            <span className="topbar-context">
              {location.pathname === '/chat'
                ? 'Agent workspace'
                : location.pathname === '/computer'
                  ? 'Live private desktop'
                : location.pathname === '/media'
                  ? 'Creative generation workspace'
                : location.pathname === '/office'
                  ? 'Documents, sheets, and presentations'
                : location.pathname.startsWith('/studio')
                  ? 'Embedded project workspace'
                  : 'Ion workspace'}
            </span>
          </div>
          <div className="topbar-actions">
            {state.gap ? <span className="reconnect-note">Restored</span> : null}
            <span
              className={`connection-pill connection-${connection}`}
              title={operator.connectionError ?? 'Secure connection status'}
            >
              <span className="connection-dot" aria-hidden="true" />
              {connectionLabel}
            </span>
          </div>
        </header>
        {children}
      </div>

      <nav className="mobile-tabbar" aria-label="Mobile navigation">
        {mainNavigation.slice(0, 3).map(([to, label, icon]) => (
          <NavLink key={to} to={to}>
            <Icon name={icon} />
            <small>{label === 'Conversations' ? 'Chats' : label}</small>
          </NavLink>
        ))}
        <button onClick={() => setMobileOpen(true)} type="button">
          <Icon name="settings" />
          <small>More</small>
        </button>
      </nav>
    </div>
  )
}

function readCookie(name: string): string {
  const prefix = `${name}=`
  for (const value of document.cookie.split(';')) {
    const trimmed = value.trim()
    if (trimmed.startsWith(prefix)) return decodeURIComponent(trimmed.slice(prefix.length))
  }
  return ''
}
