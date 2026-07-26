import {
  BrowserControlPlaneClient,
  OperatorEventStore,
  type BrowserClientOptions,
  type EventConnection,
  type OperatorState,
  type Operation,
  type ResponseEnvelope,
  type Scope,
} from '@matrixmcl/ion-shared'
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react'

type Client = BrowserControlPlaneClient

interface OperatorContextValue {
  actorID: string
  client: Client
  eventStore: OperatorEventStore
  connection: 'connecting' | 'ready' | 'degraded'
  connectionError?: string
  sessionID?: string
  setSessionID(sessionID: string | undefined): void
  command<T>(
    operation: Operation,
    payload: unknown,
    key: string,
    scope?: Omit<Scope, 'actor_id'>,
    expectedRevision?: number,
  ): Promise<ResponseEnvelope<T>>
}

const OperatorContext = createContext<OperatorContextValue | null>(null)

interface OperatorProviderProps {
  actorID: string
  children: ReactNode
  clientOptions?: BrowserClientOptions
}

export function OperatorProvider({
  actorID,
  children,
  clientOptions,
}: OperatorProviderProps) {
  const client = useMemo(
    () => new BrowserControlPlaneClient(actorID, clientOptions),
    [actorID, clientOptions],
  )
  const eventStoreRef = useRef<OperatorEventStore | null>(null)
  if (eventStoreRef.current === null) eventStoreRef.current = new OperatorEventStore()
  const eventStore = eventStoreRef.current
  const [connection, setConnection] = useState<'connecting' | 'ready' | 'degraded'>(
    'connecting',
  )
  const [connectionError, setConnectionError] = useState<string>()
  const [sessionID, setSessionID] = useState<string>()

  useEffect(() => {
    let disposed = false
    let active: EventConnection | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    let attempts = 0
    const clientID = crypto.randomUUID()
    const connect = async () => {
      if (disposed) return
      setConnection('connecting')
      try {
        active = await client.connect(eventStore.getSnapshot().last_sequence, clientID, {
          recovery(recovery) {
            eventStore.recover(recovery)
            setConnection('ready')
            setConnectionError(undefined)
            attempts = 0
          },
          event(event) {
            eventStore.accept(event)
            setConnection('ready')
          },
          degraded(error) {
            if (disposed) return
            setConnection('degraded')
            setConnectionError(error.message)
            active?.close()
            if (attempts >= 5) return
            const delay = Math.min(500 * 2 ** attempts, 5_000)
            attempts += 1
            retry = setTimeout(() => void connect(), delay)
          },
        })
        if (disposed) active.close()
      } catch (error) {
        if (disposed) return
        setConnection('degraded')
        setConnectionError(error instanceof Error ? error.message : String(error))
        if (attempts < 5) {
          const delay = Math.min(500 * 2 ** attempts, 5_000)
          attempts += 1
          retry = setTimeout(() => void connect(), delay)
        }
      }
    }
    void connect()
    return () => {
      disposed = true
      active?.close()
      if (retry !== undefined) clearTimeout(retry)
    }
  }, [client, eventStore])

  const command = useCallback(
    <T,>(
      operation: Operation,
      payload: unknown,
      key: string,
      scope: Omit<Scope, 'actor_id'> = {},
      expectedRevision?: number,
    ) => client.command<T>(operation, payload, key, scope, expectedRevision),
    [client],
  )

  const value = useMemo<OperatorContextValue>(
    () => ({
      actorID,
      client,
      eventStore,
      connection,
      ...(connectionError === undefined ? {} : { connectionError }),
      ...(sessionID === undefined ? {} : { sessionID }),
      setSessionID,
      command,
    }),
    [
      actorID,
      client,
      command,
      connection,
      connectionError,
      eventStore,
      sessionID,
    ],
  )
  return <OperatorContext.Provider value={value}>{children}</OperatorContext.Provider>
}

export function useOperator(): OperatorContextValue {
  const context = useContext(OperatorContext)
  if (context === null) throw new Error('OperatorProvider is missing')
  return context
}

export function useOperatorState(): OperatorState {
  const { eventStore } = useOperator()
  return useSyncExternalStore(
    eventStore.subscribe,
    eventStore.getSnapshot,
    eventStore.getSnapshot,
  )
}
