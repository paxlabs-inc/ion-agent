export function conciseProjectName(goal: string): string {
  const quoted = /\b(?:named|called)\s+["“]([^"”\n]{1,120})["”]/iu.exec(goal)?.[1]
    ?? /\b(?:named|called)\s+'([^'\n]{1,120})'/iu.exec(goal)?.[1]
  if (quoted !== undefined) {
    return quoted.trim()
  }
  const words = goal.trim().replaceAll(/[^\p{L}\p{N}\s-]/gu, '').split(/\s+/).filter(Boolean).slice(0, 6)
  if (words.length === 0) return ''
  const name = words.join(' ')
  return name.charAt(0).toUpperCase() + name.slice(1)
}
