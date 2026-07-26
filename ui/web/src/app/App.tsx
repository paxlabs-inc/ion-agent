import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  type CSSProperties,
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { CommandPalette } from '../components/CommandPalette'
import { OperatorLayout } from '../components/OperatorLayout'
import { PanelResizeHandle } from '../components/PanelResizeHandle'
import { ChatHost } from '../features/chat/ChatHost'
import {
  CommandCenter,
  IntegrityPage,
  SecurityPage,
  SessionsPage,
} from '../routes/CriticalRoutes'
import { SubsystemPage, subsystemSurfaces } from '../routes/SubsystemRoutes'
import { ProjectsHome, StudioWorkspace } from '../routes/StudioRoutes'
import { MediaStudio } from '../routes/MediaStudio'
import { OfficeStudio } from '../routes/OfficeStudio'
import type { BrowserClientOptions } from '@matrixmcl/ion-shared'
import {
  activeComputerTurnID,
  hasComputerHistory,
} from '../features/computer/visibility'
import {
  OperatorProvider,
  useOperator,
  useOperatorState,
} from './operator-context'
import { AuthScreen } from './AuthScreen'

const ComputerStage = lazy(async () => {
  const loaded = await import('../features/computer/ComputerStage')
  return { default: loaded.ComputerStage }
})

export function App() {
  const actorID =
    window.__ION_BOOTSTRAP__?.actor_id ??
    document
      .querySelector<HTMLMetaElement>('meta[name="ion-actor-id"]')
      ?.content.trim()
  if (actorID === undefined || actorID === '') {
    return <AuthScreen />
  }
  return <AuthenticatedApp actorID={actorID} />
}

export function AuthenticatedApp({
  actorID,
  clientOptions,
}: {
  actorID: string
  clientOptions?: BrowserClientOptions
}) {
  const queryClient = useMemo(
    () =>
      new QueryClient({
        defaultOptions: { queries: { retry: 1, staleTime: 5_000 } },
      }),
    [],
  )
  return (
    <QueryClientProvider client={queryClient}>
      <OperatorProvider
        actorID={actorID}
        {...(clientOptions === undefined ? {} : { clientOptions })}
      >
        <BrowserRouter useTransitions={false}>
          <OperatorExperience />
        </BrowserRouter>
      </OperatorProvider>
    </QueryClientProvider>
  )
}

function OperatorExperience() {
  const location = useLocation()
  const operator = useOperator()
  const operatorState = useOperatorState()
  const studioContainerRef = useRef<HTMLDivElement>(null)
  const [studioChatWidth, setStudioChatWidth] = useState(340)
  const [computerWidth, setComputerWidth] = useState(620)
  const [studioResizing, setStudioResizing] = useState(false)
  const [computerResizing, setComputerResizing] = useState(false)
  const [computerReviewSession, setComputerReviewSession] = useState<string>()
  const [computerAutoTurn, setComputerAutoTurn] = useState<string>()
  const [computerDismissedTurn, setComputerDismissedTurn] = useState<string>()
  const chatActive = location.pathname === '/chat' || location.pathname === '/'
  const computerRoute = location.pathname === '/computer'
  const studioActive =
    location.pathname === '/studio' || location.pathname.startsWith('/studio/')
  const computerScope = operator.sessionID ?? 'unscoped'
  const computerWorkingTurn = chatActive
    ? activeComputerTurnID(operatorState, operator.sessionID)
    : undefined
  const computerHistoryAvailable = chatActive &&
    hasComputerHistory(operatorState, operator.sessionID)
  const computerReviewOpen = chatActive &&
    computerReviewSession === computerScope
  const computerAutoTurnStatus = computerAutoTurn === undefined
    ? undefined
    : operatorState.turns[computerAutoTurn]?.status
  const computerAutoOpen = chatActive &&
    computerAutoTurn !== undefined &&
    computerDismissedTurn !== computerAutoTurn &&
    (
      computerWorkingTurn === computerAutoTurn ||
      computerAutoTurnStatus === 'running' ||
      computerAutoTurnStatus === 'recovering'
    )
  const computerVisible = computerRoute ||
    (chatActive && (computerAutoOpen || computerReviewOpen))

  useEffect(() => {
    if (!chatActive) {
      setComputerReviewSession(undefined)
      return
    }
    if (
      computerWorkingTurn !== undefined &&
      computerWorkingTurn !== computerDismissedTurn
    ) {
      setComputerAutoTurn(computerWorkingTurn)
    }
    if (
      computerAutoTurn !== undefined &&
      computerWorkingTurn !== computerAutoTurn &&
      computerAutoTurnStatus !== 'running' &&
      computerAutoTurnStatus !== 'recovering'
    ) {
      setComputerAutoTurn(undefined)
    }
    if (
      computerDismissedTurn !== undefined &&
      operatorState.turns[computerDismissedTurn]?.status !== 'running' &&
      operatorState.turns[computerDismissedTurn]?.status !== 'recovering'
    ) {
      setComputerDismissedTurn(undefined)
    }
  }, [
    chatActive,
    computerAutoTurn,
    computerAutoTurnStatus,
    computerDismissedTurn,
    computerWorkingTurn,
    operatorState.turns,
  ])

  const closeComputer = () => {
    const dismissedTurn = computerAutoTurn ?? computerWorkingTurn
    if (dismissedTurn !== undefined) {
      setComputerDismissedTurn(dismissedTurn)
    }
    setComputerAutoTurn(undefined)
    setComputerReviewSession(undefined)
  }

  const toggleComputer = () => {
    if (computerVisible && !computerRoute) {
      closeComputer()
      return
    }
    setComputerDismissedTurn(undefined)
    setComputerReviewSession(computerScope)
  }

  return (
    <OperatorLayout>
      <main id="main-content" tabIndex={-1}>
        <div
          className="operator-experience"
          data-computer={computerVisible}
          data-resizing={studioResizing || computerResizing}
          data-studio={studioActive}
          ref={studioContainerRef}
          style={{
            '--computer-width': `${String(computerWidth)}px`,
            '--studio-chat-width': `${String(studioChatWidth)}px`,
          } as CSSProperties}
        >
          <ChatHost
            active={chatActive || studioActive || computerRoute}
            computerReviewAvailable={computerHistoryAvailable}
            computerReviewOpen={computerVisible && !computerRoute}
            mode={studioActive ? 'studio' : 'full'}
            onComputerReviewToggle={toggleComputer}
          />
          {studioActive ? (
            <PanelResizeHandle
              containerRef={studioContainerRef}
              defaultValue={340}
              direction="from-left"
              label="Resize Studio conversation"
              max={520}
              min={280}
              onChange={setStudioChatWidth}
              onResizeStateChange={setStudioResizing}
              oppositeMinimum={560}
              value={studioChatWidth}
            />
          ) : null}
          {computerVisible ? (
            <PanelResizeHandle
              containerRef={studioContainerRef}
              defaultValue={620}
              direction="from-right"
              label="Resize Computer stage"
              max={900}
              min={380}
              onChange={setComputerWidth}
              onResizeStateChange={setComputerResizing}
              oppositeMinimum={390}
              value={computerWidth}
            />
          ) : null}
          <Suspense
            fallback={
              <section
                aria-label="Computer"
                className="computer-stage"
                data-active={computerVisible}
              />
            }
          >
            <ComputerStage
              active={computerVisible}
              {...(chatActive ? { onClose: closeComputer } : {})}
            />
          </Suspense>
          <div className="route-host" hidden={chatActive || computerRoute}>
          <Routes>
            <Route path="/" element={<Navigate replace to="/chat" />} />
            <Route path="/chat" element={null} />
            <Route path="/computer" element={null} />
            <Route path="/media" element={<MediaStudio />} />
            <Route path="/office" element={<OfficeStudio />} />
            <Route path="/overview" element={<CommandCenter />} />
            <Route path="/sessions" element={<SessionsPage />} />
            <Route path="/security" element={<SecurityPage />} />
            <Route path="/integrity" element={<IntegrityPage />} />
            <Route path="/projects" element={<ProjectsHome />} />
            <Route path="/studio" element={<ProjectsHome />} />
            <Route path="/studio/:projectID" element={<StudioWorkspace />} />
            {subsystemSurfaces.map((surface) => (
              <Route
                key={surface.path}
                path={surface.path}
                element={<SubsystemPage surface={surface} />}
              />
            ))}
            <Route path="*" element={<Navigate replace to="/" />} />
          </Routes>
          </div>
        </div>
      </main>
      <CommandPalette />
    </OperatorLayout>
  )
}
