import type { OperatorState } from '@matrixmcl/ion-shared'

function isComputerSurfaceTool(tool?: string, operation?: string): boolean {
  return [tool, operation].some((candidate) => {
    const normalized = candidate?.trim().toLowerCase()
    return normalized?.startsWith('browser_') === true ||
      normalized?.startsWith('browser.') === true ||
      normalized?.startsWith('computer_') === true ||
      normalized?.startsWith('computer.') === true
  })
}

function activeComputerActivities(
  state: OperatorState,
  sessionID?: string,
) {
  return Object.values(state.computer_activities).filter((activity) => {
    if (
      activity.terminal ||
      activity.unsupported ||
      activity.payload === undefined ||
      !isComputerSurfaceTool(activity.tool, activity.operation)
    ) {
      return false
    }
    return sessionID === undefined ||
      activity.payload.scope.session_id === sessionID
  })
}

export function hasActiveComputerActivity(
  state: OperatorState,
  sessionID?: string,
): boolean {
  return activeComputerActivities(state, sessionID).length > 0
}

export function activeComputerTurnID(
  state: OperatorState,
  sessionID?: string,
): string | undefined {
  return activeComputerActivities(state, sessionID)
    .sort((left, right) => right.last_sequence - left.last_sequence)
    .find((activity) => activity.payload?.scope.turn_id !== undefined)
    ?.payload?.scope.turn_id
}

export function hasComputerHistory(
  state: OperatorState,
  sessionID?: string,
): boolean {
  return state.recent_events.some((event) =>
    event.type.startsWith('tool.') &&
    (sessionID === undefined || event.correlation.session_id === sessionID)
  )
}
