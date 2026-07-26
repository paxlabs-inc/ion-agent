import { expect, test } from '@playwright/test'

const surfaces = [
  ['/knowledge', 'What Ion knows'],
  ['/work', 'Goals and active work'],
  ['/execution', 'Actions and decisions'],
  ['/extensions', 'Models and connections'],
  ['/presence', 'Where and when Ion works'],
  ['/identity', 'Preferences and identity'],
  ['/diagnostics', 'System health and activity'],
] as const

const nonChatRoutes = [
  '/overview',
  '/sessions',
  '/security',
  '/integrity',
  ...surfaces.map(([path]) => path),
] as const

test('chat shell keeps a focused hierarchy in dark mode', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.goto('/chat')

  await expect(page.getByText('What can I do for you?')).toBeVisible()
  await expect(page.locator('.empty-conversation .composer')).toBeVisible()
  await expect(page.getByText('Full agent')).toBeVisible()
  await expect(page.locator('.shell-sidebar')).toHaveCSS(
    'background-color',
    'oklch(0.205 0.004 205.76)',
  )
  await expect(page.locator('html')).toHaveCSS(
    'background-color',
    'oklch(0.174 0.004 205.76)',
  )
  const composerInput = page.getByLabel('Message Ion')
  await composerInput.focus()
  const composerFocus = await composerInput.evaluate((element) => {
    const inputStyle = getComputedStyle(element)
    const composerStyle = getComputedStyle(element.closest('.composer')!)
    return {
      inputShadow: inputStyle.boxShadow,
      outline: inputStyle.outlineStyle,
      composerShadow: composerStyle.boxShadow,
    }
  })
  expect(composerFocus.inputShadow).toBe('none')
  expect(composerFocus.outline).toBe('none')
  expect(composerFocus.composerShadow).not.toContain('0px 0px 0px 3px')
  await expect(page.locator('.technical-details[open]')).toHaveCount(0)
})

test('primary subsystem read matrix is truthful and responsive', async ({
  page,
}, testInfo) => {
  for (const [index, [path, heading]] of surfaces.entries()) {
    if (index === 0) {
      await page.goto(path)
    } else {
      await page.evaluate((nextPath) => {
        window.history.pushState({}, '', nextPath)
        window.dispatchEvent(new PopStateEvent('popstate'))
      }, path)
    }
    await expect(page.getByRole('heading', { name: heading })).toBeVisible()
    await expect(
      page.getByText(/Up to date|Setup needed|\d+ sections? need(?:s)? attention/),
    ).toBeVisible()
    await expect(page.locator('.data-surface > header p').first()).toBeVisible()
    await expect(page.locator('.technical-details[open]')).toHaveCount(0)
    await expect(page.locator('.surface-content > .structured-list code')).toHaveCount(0)
    for (const card of await page.locator('.data-surface.state-error:visible').all()) {
      await expect(card.getByText('This information is unavailable')).toBeVisible()
      await expect(card.getByText('Technical details')).toBeVisible()
    }
    if (testInfo.project.name === 'chromium-mobile') {
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      )
      expect(overflow, `${path} horizontal overflow`).toBeLessThanOrEqual(1)
    }
  }
  await page.evaluate(() => {
    window.history.pushState({}, '', '/extensions')
    window.dispatchEvent(new PopStateEvent('popstate'))
  })
  await expect(page.getByTitle('Sandboxed plugin surface')).toHaveCount(0)
})

test('every non-chat page keeps real content inside a balanced responsive grid', async ({
  page,
}, testInfo) => {
  for (const path of nonChatRoutes) {
    await page.goto(path)
    await expect(page.locator('.route-page')).toBeVisible()

    const geometry = await page.evaluate(() => {
      const cards = Array.from(document.querySelectorAll<HTMLElement>('.data-surface'))
      const grids = Array.from(document.querySelectorAll<HTMLElement>('.surface-grid'))
      const wideCards = cards.filter((card) => card.classList.contains('surface-wide'))
      const artificialSpace = cards.map((card) => {
        const childrenHeight = Array.from(card.children).reduce(
          (total, child) => total + (child as HTMLElement).getBoundingClientRect().height,
          0,
        )
        return card.getBoundingClientRect().height - childrenHeight
      })
      const narrowDenseItems = Array.from(
        document.querySelectorAll<HTMLElement>('.readable-list-grid > .readable-record'),
      ).filter((item) => item.getBoundingClientRect().width < 250).length
      const rightAlignedValues = Array.from(
        document.querySelectorAll<HTMLElement>('.structured-data dd'),
      ).filter((item) => getComputedStyle(item).textAlign === 'right').length
      return {
        documentOverflow: document.documentElement.scrollWidth - window.innerWidth,
        routeOverflow: Array.from(document.querySelectorAll<HTMLElement>('.route-page')).some(
          (route) => route.scrollWidth - route.clientWidth > 1,
        ),
        maximumArtificialSpace: artificialSpace.length === 0 ? 0 : Math.max(...artificialSpace),
        narrowDenseItems,
        rightAlignedValues,
        wideCardsFitGrid: wideCards.every((card) => {
          const grid = card.closest<HTMLElement>('.surface-grid')
          return grid === null || Math.abs(grid.getBoundingClientRect().width - card.getBoundingClientRect().width) <= 2
        }),
        gridsHaveVisibleWidth: grids.every((grid) => grid.getBoundingClientRect().width > 0),
      }
    })

    expect(geometry.documentOverflow, `${path} document overflow`).toBeLessThanOrEqual(1)
    expect(geometry.routeOverflow, `${path} route overflow`).toBe(false)
    expect(geometry.maximumArtificialSpace, `${path} stretched card space`).toBeLessThanOrEqual(3)
    expect(geometry.rightAlignedValues, `${path} right-aligned record values`).toBe(0)
    expect(geometry.wideCardsFitGrid, `${path} dense card width`).toBe(true)
    expect(geometry.gridsHaveVisibleWidth, `${path} grid width`).toBe(true)
    if (testInfo.project.name === 'chromium-desktop') {
      expect(geometry.narrowDenseItems, `${path} squeezed dense list items`).toBe(0)
    }
  }
})

test('knowledge overview uses plain language and keeps raw records optional', async ({
  page,
}) => {
  await page.goto('/knowledge')
  const savedKnowledge = page.locator('.data-surface').filter({
    has: page.getByRole('heading', { name: 'Saved knowledge' }),
  })
  const unavailable = savedKnowledge.getByText('Unavailable', { exact: true })
  if (await unavailable.count() > 0) {
    await expect(unavailable).toBeVisible()
    await expect(
      savedKnowledge.getByText('This information is unavailable'),
    ).toBeVisible()
  } else {
    await expect(
      savedKnowledge.getByText(/No saved knowledge yet|Explicitly saved/).first(),
    ).toBeVisible()
  }

  const assumptions = page.locator('.data-surface').filter({
    has: page.getByRole('heading', { name: 'Assumptions' }),
  })
  await expect(assumptions.getByText('Technical details')).toBeVisible()
  await expect(assumptions.locator('.technical-details')).not.toHaveAttribute('open', '')
  await expect(assumptions.locator('.structured-list code')).toHaveCount(0)
})

test('software projects and workspace hosts use the shared production surface', async ({ page }) => {
  await page.goto('/work')
  const projects = page.locator('.data-surface').filter({
    has: page.getByRole('heading', { name: 'Software projects' }),
  })
  const hosts = page.locator('.data-surface').filter({
    has: page.getByRole('heading', { name: 'Workspace hosts' }),
  })
  await expect(projects).toBeVisible()
  await expect(hosts).toBeVisible()
  await expect(projects.getByText('This information is unavailable')).toHaveCount(0)
  await expect(hosts.getByText('ion.workspace-host.v1', { exact: true })).toBeVisible()
  await expect(page.locator('.technical-details[open]')).toHaveCount(0)
})

test('Wide Work shows live progress, attention, steering, and cancellation', async ({
  page,
}) => {
  const seeded = await page.request.post('/e2e/wide-work')
  expect(seeded.ok()).toBe(true)
  const seededRun = await seeded.json() as { id: string }
  await page.goto('/work')
  const supervisor = page.locator('.data-surface').filter({
    has: page.getByRole('heading', { name: 'Agent supervisor' }),
  })
  const activeRun = supervisor.locator(
    `.supervisor-run[data-supervisor-id="${seededRun.id}"]`,
  )
  await expect(activeRun).toBeVisible()
  await expect(
    activeRun.getByText('1 workstream need a decision or verified evidence.'),
  ).toBeVisible()
  await expect(
    activeRun.getByRole('progressbar', { name: 'Wide Work stream 01 progress' }),
  ).toHaveAttribute('value', '5')

  await activeRun
    .getByLabel('Guidance for this outcome')
    .fill('Prioritize the current verification manifest.')
  await activeRun.getByRole('button', { name: 'Add guidance' }).click()
  await expect(
    page.getByText('Guidance added to the active supervised outcome.'),
  ).toBeVisible()

  await activeRun
    .getByRole('button', { name: 'Cancel supervised work' })
    .click()
  await expect(activeRun.getByRole('heading', { name: 'Cancelled' })).toBeVisible()
  await expect(
    activeRun.getByRole('button', { name: 'Cancel supervised work' }),
  ).toHaveCount(0)
  await expect(activeRun.getByText('0 active / 20 lanes')).toBeVisible()
})

test('Software Studio reviews and applies the real authoritative specification', async ({ page }) => {
  await page.goto('/work')
  const studio = page.locator('.data-surface').filter({
    has: page.getByRole('heading', { name: 'Specifications to review' }),
  })
  await expect(studio).toBeVisible()
  await expect(studio.getByRole('heading', { name: 'Add a welcoming project page' })).toBeVisible()
  await expect(studio.getByText('Reversible assumption: Keep the existing page hierarchy', { exact: true })).toBeVisible()
  const accept = studio.getByRole('button', { name: 'Accept specification' })
  if (await accept.count() > 0) {
    await accept.focus()
    await expect(accept).toBeFocused()
    await accept.click()
    const apply = studio.getByRole('button', { name: 'Apply to authoritative spec' })
    await expect(apply).toBeVisible()
    await apply.click()
  }
  await expect(studio.getByText('Applied to spec')).toBeVisible()
  await expect(page.locator('.technical-details[open]')).toHaveCount(0)
})

test('provider setup is task-first and does not collect credentials in JavaScript', async ({ page }) => {
  await page.goto('/extensions')
  const secret = `browser-secret-${crypto.randomUUID()}`
  await expect(page.getByText('Connect a model before starting agent work')).toBeVisible()
  await expect(
    page.getByText(/Browser control is ready; agent email needs setup|Browser workflow setup is incomplete/),
  ).toBeVisible()
  await page.getByText('Technical setting names').click()
  await expect(page.getByText(/PROVIDER_NAME/)).toBeVisible()
  await page.getByText('Protected setting names').click()
  await expect(page.getByText(/MACHINE_MAIL_API_KEY/)).toBeVisible()
  await expect(page.getByLabel('Connection key name')).toHaveCount(0)
  await expect(page.getByLabel('Private value')).toHaveCount(0)
  await expect(page.locator('input[type="password"]')).toHaveCount(0)
  await expect(page.getByText(secret)).toHaveCount(0)
  const persistence = await page.evaluate(() => ({
    local: Object.values(localStorage),
    session: Object.values(sessionStorage),
    html: document.documentElement.outerHTML,
  }))
  expect(persistence.local).not.toContain(secret)
  expect(persistence.session).not.toContain(secret)
  expect(persistence.html).not.toContain(secret)
})
