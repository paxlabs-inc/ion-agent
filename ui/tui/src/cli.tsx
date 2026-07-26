import { render } from 'ink'
import React from 'react'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import {
  ControlPlaneClient,
  OperatorEventStore,
  type OperatorState,
} from '@matrixmcl/ion-shared'
import { App } from './App.js'
import { LocalControlPlaneTransport } from './local-client.js'

const options = parseArguments(process.argv.slice(2))
const store = new OperatorEventStore()
let connection: 'ready' | 'degraded' = 'ready'
let connectionError: string | undefined
let rerender: ((tree: React.ReactNode) => void) | undefined
let transport: LocalControlPlaneTransport

const draw = () => {
  if (rerender === undefined || transport === undefined) return
  rerender(
    <App
      connection={connection}
      {...(connectionError === undefined ? {} : { error: connectionError })}
      onOpenEditor={openEditor}
      state={store.getSnapshot()}
      transport={transport}
    />,
  )
}

transport = await LocalControlPlaneTransport.connect(
  options.socket,
  options.capability,
  {
    recovery(recovery) {
      store.recover(recovery)
      draw()
    },
    event(event) {
      store.accept(event)
      transport.acknowledge(options.clientID, event.sequence)
      draw()
    },
    degraded(error) {
      connection = 'degraded'
      connectionError = redactError(error.message)
      draw()
    },
  },
  options.timeout,
)
if (options.check) {
  const client = new ControlPlaneClient(transport.actorID, transport)
  const catalog = await client.query('commands.catalog', {})
  if (catalog.error !== undefined) throw new Error(catalog.error.message)
  const system = await client.query('system.health', {})
  if (system.error !== undefined) throw new Error(system.error.message)
  if (
    typeof system.result !== 'object' || system.result === null ||
    !('status' in system.result) || system.result.status !== 'ready'
  ) {
    throw new Error('authenticated system health is not ready')
  }
  const channels = await client.query('channel.health', {})
  if (channels.error !== undefined) throw new Error(channels.error.message)
  const schedules = await client.query('schedule.list', {})
  if (
    schedules.error !== undefined || !Array.isArray(schedules.result) ||
    !schedules.result.some((item) => (
      typeof item === 'object' && item !== null &&
      'source' in item && item.source === 'agent_scheduler' &&
      'status' in item && item.status === 'ready'
    ))
  ) {
    throw new Error(schedules.error?.message ?? 'agent scheduler is not ready')
  }
  const toolReadiness = await client.query('tool.readiness', {})
  const toolStatuses = (
    typeof toolReadiness.result === 'object' && toolReadiness.result !== null &&
    'tools' in toolReadiness.result && Array.isArray(toolReadiness.result.tools)
  ) ? toolReadiness.result.tools : []
  for (const name of [
    'schedule_create',
    'schedule_list',
    'schedule_get',
    'schedule_cancel',
  ]) {
    if (!toolStatuses.some((item) => (
      typeof item === 'object' && item !== null &&
      'name' in item && item.name === name &&
      'ready' in item && item.ready === true
    ))) {
      throw new Error(toolReadiness.error?.message ?? `${name} is not ready`)
    }
  }
	const projects = await client.query('project.list', {})
	if (
	  projects.error !== undefined || typeof projects.result !== 'object' ||
	  projects.result === null || !('projects' in projects.result) ||
	  !Array.isArray(projects.result.projects)
	) {
	  throw new Error(projects.error?.message ?? 'authenticated project registry is unavailable')
	}
	const workspaceHosts = await client.query('workspace.capabilities', {})
	if (
	  workspaceHosts.error !== undefined || typeof workspaceHosts.result !== 'object' ||
	  workspaceHosts.result === null ||
	  !('contract_version' in workspaceHosts.result) ||
	  workspaceHosts.result.contract_version !== 'ion.workspace-host.v1'
	) {
	  throw new Error(workspaceHosts.error?.message ?? 'workspace host negotiation is unavailable')
	}
	const studio = await client.query('studio.intent.list', {})
	if (
	  studio.error !== undefined || typeof studio.result !== 'object' || studio.result === null ||
	  !('intents' in studio.result) || !Array.isArray(studio.result.intents)
	) {
	  throw new Error(studio.error?.message ?? 'Software Studio specification state is unavailable')
  }
  transport.close()
  process.stdout.write('Ion terminal client is ready; system, scheduler, channel, project, workspace, and Studio health answered.\n')
} else {
  transport.subscribe(0)
  const alternateScreen = process.stdout.isTTY && process.env.TERM !== 'dumb'
  if (alternateScreen) process.stdout.write('\u001B[?1049h\u001B[?25l')
  const instance = render(
    <App
      connection={connection}
      onOpenEditor={openEditor}
      state={store.getSnapshot()}
      transport={transport}
    />,
    { exitOnCtrlC: true, patchConsole: false },
  )
  rerender = instance.rerender

  const restore = () => {
    transport.close()
    if (alternateScreen) process.stdout.write('\u001B[?25h\u001B[?1049l\u001B[0m')
  }
  process.once('SIGTERM', () => {
    restore()
    process.exit(143)
  })
  process.once('SIGHUP', () => {
    restore()
    process.exit(129)
  })
  await instance.waitUntilExit()
  restore()
}

interface Options {
  socket: string
  capability: string
  clientID: string
  timeout: number
  check: boolean
}

function parseArguments(arguments_: string[]): Options {
  const values = new Map<string, string>()
  for (let index = 0; index < arguments_.length; index += 2) {
    const name = arguments_[index]
    const value = arguments_[index + 1]
    if (name === undefined || value === undefined || !name.startsWith('--')) {
      throw new Error('usage: ion-tui --socket PATH --capability TOKEN')
    }
    values.set(name, value)
  }
  const socket = values.get('--socket')
  const capability = values.get('--capability')
  if (socket === undefined || capability === undefined) {
    throw new Error('usage: ion-tui --socket PATH --capability TOKEN')
  }
  return {
    socket,
    capability,
    clientID: values.get('--client-id') ?? crypto.randomUUID(),
    timeout: Number(values.get('--timeout-ms') ?? '5000'),
    check: values.get('--check') === 'true',
  }
}

async function openEditor(current: string): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), 'ion-tui-'))
  const path = join(directory, 'message.md')
  try {
    await writeFile(path, current, { mode: 0o600 })
    const editor = process.env.EDITOR ?? process.env.VISUAL ?? 'vi'
    spawnSync(editor, [path], { stdio: 'inherit' })
    return await readFile(path, 'utf8')
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
}

function redactError(message: string): string {
  return message
    .replaceAll(/Bearer\s+\S+/gi, 'Bearer [REDACTED]')
    .replaceAll(/(token|capability|secret)=\S+/gi, '$1=[REDACTED]')
}
