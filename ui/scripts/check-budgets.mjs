import { readdir, readFile, stat } from 'node:fs/promises'
import { gzipSync } from 'node:zlib'
import { join, relative, resolve } from 'node:path'

const root = resolve(import.meta.dirname, '..')
const web = join(root, 'web', 'dist')
const tui = join(root, 'tui', 'dist', 'ion-tui.mjs')
const files = await walk(web)
const javascript = files.filter((path) => path.endsWith('.js'))
const compressed = (
  await Promise.all(javascript.map(async (path) => gzipSync(await readFile(path)).length))
).reduce((sum, size) => sum + size, 0)
const tuiSize = (await stat(tui)).size
const sourceMaps = files.filter((path) => path.endsWith('.map'))

assertBudget(
  compressed <= 350 * 1024,
  `initial JavaScript is ${format(compressed)} compressed; budget is 350 KiB`,
)
assertBudget(
  tuiSize <= 3 * 1024 * 1024,
  `bundled TUI is ${format(tuiSize)}; budget is 3 MiB`,
)
assertBudget(
  sourceMaps.length === 0,
  `production bundle contains source maps: ${sourceMaps.map((path) => relative(root, path)).join(', ')}`,
)

console.log(`OK: Web initial JavaScript: ${format(compressed)} / 350 KiB`)
console.log(`OK: Bundled TUI artifact: ${format(tuiSize)} / 3 MiB`)
console.log('OK: Production source maps: none')

async function walk(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const nested = await Promise.all(
    entries.map((entry) => {
      const path = join(directory, entry.name)
      return entry.isDirectory() ? walk(path) : [path]
    }),
  )
  return nested.flat()
}

function assertBudget(condition, message) {
  if (!condition) throw new Error(message)
}

function format(bytes) {
  return `${(bytes / 1024).toFixed(1)} KiB`
}
