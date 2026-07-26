import { spawnSync } from 'node:child_process'
import { readdirSync, readFileSync } from 'node:fs'
import { extname, join } from 'node:path'

const waivers = new Map([
  [
    'GHSA-qwww-vcr4-c8h2',
    {
      dependency: 'react-router',
      expires: '2026-08-31',
      reason: 'Ion is a client-only SPA and does not use React Router unstable RSC APIs.',
    },
  ],
])

const sourceExtensions = new Set(['.js', '.jsx', '.mjs', '.ts', '.tsx'])
const rscAPI = /\bunstable_(?:[A-Za-z0-9_]*RSC[A-Za-z0-9_]*|createCallServer|routeRSCServerRequest|matchRSCServerRequest)\b/

function findRSCUsage(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      const found = findRSCUsage(path)
      if (found) return found
      continue
    }
    if (sourceExtensions.has(extname(entry.name)) && rscAPI.test(readFileSync(path, 'utf8'))) {
      return path
    }
  }
  return ''
}

const audit = spawnSync(
  'npm',
  ['audit', '--omit=dev', '--audit-level=moderate', '--json'],
  { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 },
)

let report
try {
  report = JSON.parse(audit.stdout)
} catch {
  process.stderr.write(audit.stderr)
  console.error('production dependency audit did not return valid JSON')
  process.exit(1)
}

if (report.error) {
  console.error(report.error)
  process.exit(1)
}

const unresolved = []
const appliedWaivers = new Set()

for (const [dependency, vulnerability] of Object.entries(report.vulnerabilities ?? {})) {
  for (const advisory of vulnerability.via ?? []) {
    if (typeof advisory === 'string') continue
    const advisoryID = advisory.url?.split('/').pop() ?? String(advisory.source)
    const waiver = waivers.get(advisoryID)
    if (!waiver || waiver.dependency !== dependency) {
      unresolved.push({ dependency, advisoryID, advisory })
      continue
    }
    if (new Date(`${waiver.expires}T23:59:59Z`) < new Date()) {
      unresolved.push({ dependency, advisoryID, advisory, expired: waiver.expires })
      continue
    }
    const rscUsage = findRSCUsage(join(process.cwd(), 'web', 'src'))
    if (rscUsage) {
      unresolved.push({ dependency, advisoryID, advisory, rscUsage })
      continue
    }
    appliedWaivers.add(advisoryID)
    console.log(`waived ${advisoryID} through ${waiver.expires}: ${waiver.reason}`)
  }
}

if (unresolved.length > 0) {
  for (const finding of unresolved) {
    const detail = finding.expired
      ? `waiver expired ${finding.expired}`
      : finding.rscUsage
        ? `affected API found in ${finding.rscUsage}`
        : finding.advisory.title
    console.error(`${finding.dependency}: ${finding.advisoryID}: ${detail}`)
  }
  process.exit(1)
}

if (audit.status !== 0 && appliedWaivers.size === 0) {
  process.stderr.write(audit.stderr)
  console.error('production dependency audit failed without an attributable advisory')
  process.exit(1)
}

console.log('production dependency audit passed')
