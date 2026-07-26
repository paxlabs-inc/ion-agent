import { expect, test } from '@playwright/test'

test('the persistent Studio composer safely creates and binds a new project', async ({ page }) => {
  await page.goto('/studio')

  await expect(page.getByText('New Studio project')).toBeVisible()
  await page.getByLabel('Message Ion').fill('Show a delayed streaming response for a bounded composer project')
  await page.getByRole('button', { name: 'Send' }).click()

  await expect(page).toHaveURL(/\/studio\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: 'Show a delayed streaming response for' })).toBeVisible()
  await expect(page.getByText('Studio project', { exact: true })).toBeVisible()
  await expect(page.getByText('without duplicate messages.')).toBeVisible()
})

test('Software Studio opens a real project workspace with the persistent conversation', async ({ page }) => {
  await page.goto('/studio')

  await expect(page.getByRole('heading', { name: 'What do you want to build or change?' })).toBeVisible()
  await expect(page.getByLabel('Your outcome')).toBeVisible()
  await expect(page.getByText('Private values stay in the vault; this form accepts references only.')).toBeVisible()
  await expect(page.getByRole('button', { name: /Welcome project/ })).toBeVisible()

  await page.getByRole('button', { name: /Welcome project/ }).click()
  await expect(page).toHaveURL(/\/studio\/[0-9a-f-]+$/)
  await expect(page.getByTestId('persistent-chat-host')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Welcome project' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Add a welcoming project page' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Current outcome' })).toBeVisible()
  await expect(page.getByText('The welcome message is visible')).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Plan' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('tab')).toHaveCount(10)
})

test('the desktop shell retracts to a useful navigation rail', async ({ page }, testInfo) => {
  await page.goto('/chat')
  const collapse = page.getByRole('button', { name: 'Collapse sidebar' })
  if (testInfo.project.name === 'chromium-mobile') {
    await expect(collapse).toBeHidden()
    return
  }

  const shell = page.locator('.operator-shell')
  const sidebar = page.locator('.shell-sidebar')
  const expandedWidth = (await sidebar.boundingBox())?.width ?? 0
  await collapse.click()
  await expect(shell).toHaveAttribute('data-sidebar-collapsed', 'true')
  await expect(page.getByRole('button', { name: 'Expand sidebar' })).toBeVisible()
  await expect(page.locator('.shell-sidebar').getByTitle('Software Studio')).toBeVisible()
  const collapsedWidth = (await sidebar.boundingBox())?.width ?? expandedWidth
  expect(collapsedWidth).toBeLessThan(expandedWidth)

  await page.getByRole('button', { name: 'Expand sidebar' }).click()
  await expect(shell).toHaveAttribute('data-sidebar-collapsed', 'false')
})

test('Studio tools remain honest, keyboard operable, responsive, and theme-safe', async ({ page }, testInfo) => {
  await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' })
  await page.goto('/studio')
  await page.getByRole('button', { name: /Welcome project/ }).click()

  if (testInfo.project.name === 'chromium-desktop') {
    const conversationResize = page.getByRole('separator', { name: 'Resize Studio conversation' })
    const conversationBefore = Number(await conversationResize.getAttribute('aria-valuenow'))
    await conversationResize.focus()
    await conversationResize.press('ArrowRight')
    await expect(conversationResize).toHaveAttribute(
      'aria-valuenow',
      String(conversationBefore + 24),
    )

    const contextResize = page.getByRole('separator', { name: 'Resize project context' })
    const contextBefore = Number(await contextResize.getAttribute('aria-valuenow'))
    await contextResize.focus()
    await contextResize.press('ArrowLeft')
    await expect(contextResize).toHaveAttribute('aria-valuenow', String(contextBefore + 24))

    const bounds = await contextResize.boundingBox()
    if (bounds !== null) {
      await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + 80)
      await page.mouse.down()
      await page.mouse.move(bounds.x - 36, bounds.y + 80)
      await page.mouse.up()
      expect(Number(await contextResize.getAttribute('aria-valuenow'))).toBeGreaterThan(
        contextBefore + 24,
      )
    }
    await contextResize.dblclick()
    await expect(contextResize).toHaveAttribute('aria-valuenow', '280')
  }

  const plan = page.getByRole('tab', { name: 'Plan' })
  await plan.focus()
  await plan.press('ArrowRight')
  await expect(page.getByRole('tab', { name: 'Changes' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('heading', { name: 'Changes' })).toBeVisible()

  await page.getByRole('tab', { name: 'Code' }).click()
  await expect(page.getByRole('heading', { name: 'Code' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Refresh index' })).toBeVisible()

  await page.getByRole('tab', { name: 'Terminal' }).click()
  await expect(page.getByRole('heading', { name: 'Terminal' })).toBeVisible()
  await expect(page.getByLabel('Executable')).toBeVisible()
  await expect(page.getByText('no shell interpolation is used')).toBeVisible()

  await page.getByRole('tab', { name: 'Preview' }).click()
  await expect(page.getByLabel('Preview service')).toHaveValue('web')
  const startPreview = page.getByRole('button', { name: 'Start preview' })
  if (await startPreview.isVisible()) await startPreview.click()
  await expect(page.getByText('running', { exact: true })).toBeVisible({ timeout: 35_000 })
  const preview = page.getByTitle('Isolated project preview')
  await expect(preview).toBeVisible()
  await expect(preview).toHaveAttribute('sandbox', /allow-same-origin/)
  await expect(preview).not.toHaveAttribute('sandbox', /allow-popups|allow-top-navigation/)
  await expect(page.frameLocator('iframe[title="Isolated project preview"]').locator('body')).toContainText('Built with Ion')
  await expect(page.getByTestId('persistent-chat-host')).toBeVisible()
  await page.getByRole('button', { name: 'Reload', exact: true }).click()
  await expect(page.getByTestId('persistent-chat-host')).toBeVisible()
  await page.getByRole('button', { name: 'Stop', exact: true }).click()
  await expect(page.getByText('stopped', { exact: true })).toBeVisible()
  await expect(page.getByText('Start the service when another live preview is needed.')).toBeVisible()

  await page.getByRole('tab', { name: 'Tests' }).click()
  await expect(page.getByRole('heading', { name: 'Tests' })).toBeVisible()
  const prepareVerification = page.getByRole('button', { name: /Prepare verification|Refresh verification/ })
  if (await prepareVerification.isVisible()) await prepareVerification.click()
  await expect(page.getByRole('button', { name: 'Run required gates' })).toBeVisible()
  await expect(page.locator('#studio-panel-tests .diagnostic-list .studio-status').filter({ hasText: /Not Run|Passed/ })).toBeVisible()
  await page.getByRole('button', { name: 'Run required gates' }).click()
  await expect(page.getByRole('status')).toContainText('Verification finished: Passed.')
  await expect(page.getByText(/1 covered · 0 uncovered/)).toBeVisible()

  await page.getByRole('tab', { name: 'Problems' }).click()
  await expect(page.getByRole('heading', { name: 'Problems' })).toBeVisible()
  await expect(page.getByText('runtime process exited unexpectedly')).toHaveCount(0)

  await page.getByRole('tab', { name: 'Data' }).click()
  await expect(page.getByText('No resource is connected')).toBeVisible()
  await expect(page.getByText('Write-only configuration')).toBeVisible()

  await page.getByRole('tab', { name: 'Deploy' }).click()
  await expect(page.getByRole('heading', { name: 'Preview, staging, and production' })).toBeVisible()
  await expect(page.getByText('Readiness and portable handoff')).toBeVisible()

  const geometry = await page.evaluate(() => ({
    documentOverflow: document.documentElement.scrollWidth - window.innerWidth,
    workspaceOverflow: document.querySelector<HTMLElement>('.studio-workspace')?.scrollWidth ?? 0,
    workspaceWidth: document.querySelector<HTMLElement>('.studio-workspace')?.clientWidth ?? 0,
    scheme: getComputedStyle(document.documentElement).colorScheme,
  }))
  expect(geometry.documentOverflow).toBeLessThanOrEqual(1)
  expect(geometry.workspaceOverflow - geometry.workspaceWidth).toBeLessThanOrEqual(1)
  expect(geometry.scheme).toBe('dark')
  if (testInfo.project.name === 'chromium-mobile') {
    await expect(page.getByTestId('persistent-chat-host')).toBeVisible()
  }
})

test('a first-time Studio journey reaches verified staging and preserves field-general continuity', async ({ page }, testInfo) => {
  test.setTimeout(90_000)
  test.skip(testInfo.project.name === 'chromium-mobile', 'The responsive path is covered by the full mobile Studio matrix.')

  await page.goto('/studio')
  await page.getByRole('button', { name: /Welcome project/ }).click()

  await page.getByLabel(/Plan comment/).fill('Keep the calm hierarchy and make the result easy to verify.')
  await page.getByRole('button', { name: 'Accept plan' }).click()
  await expect(page.getByRole('status')).toContainText('Specification accepted.')
  await page.getByRole('button', { name: 'Apply to authoritative spec' }).click()
  await expect(page.getByRole('status')).toContainText('authoritative')

  await page.getByRole('tab', { name: 'Changes' }).click()
  await page.locator('.change-file').filter({ hasText: 'index.html' }).locator('summary strong').click()
  await expect(page.getByText('A calm first project is ready to review.')).toBeVisible()
  await page.getByLabel('Comment').fill('Preserve this readable result through release.')
  await page.getByRole('button', { name: 'Add review comment' }).click()
  await expect(page.getByText('Preserve this readable result through release.')).toBeVisible()

  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+K' : 'Control+K')
  await expect(page.getByRole('dialog', { name: 'Search Ion' })).toBeVisible()
  await page.getByRole('button', { name: 'Open project Terminal' }).click()
  await expect(page).toHaveURL(/\?panel=terminal$/)
  await page.getByLabel('Executable').fill('/usr/bin/printf')
  await page.getByLabel(/Arguments/).fill('studio-terminal-proof')
  await page.getByRole('button', { name: 'Run command' }).click()
  await expect(page.getByLabel('Terminal output')).toContainText('studio-terminal-proof')

  await page.getByRole('tab', { name: 'Preview' }).click()
  const startPreview = page.getByRole('button', { name: 'Start preview' })
  if (await startPreview.isVisible()) await startPreview.click()
  await expect(page.frameLocator('iframe[title="Isolated project preview"]').locator('body')).toContainText('A calm first project is ready to review.')
  await page.getByRole('button', { name: 'Capture & inspect' }).click()
  await page.getByLabel('Annotation').fill('The live result matches the accepted calm hierarchy.')
  await page.getByRole('button', { name: 'Save annotation' }).click()
  await expect(page.getByRole('status')).toContainText('Visual annotation saved')

  await page.getByRole('tab', { name: 'Tests' }).click()
  const prepareVerification = page.getByRole('button', { name: /Prepare verification|Refresh verification/ })
  if (await prepareVerification.isVisible()) await prepareVerification.click()
  await page.getByRole('button', { name: 'Run required gates' }).click()
  await expect(page.getByRole('status')).toContainText('Verification finished: Passed.')
  await expect(page.getByText(/1 covered · 0 uncovered/)).toBeVisible()

  await page.getByRole('tab', { name: 'Deploy' }).click()
  await page.getByText('Prepare a deployment').click()
  await page.getByRole('button', { name: 'Build immutable plan' }).click()
  await expect(page.getByRole('button', { name: 'Deploy reviewed artifact' })).toBeVisible()
  await page.getByRole('button', { name: 'Deploy reviewed artifact' }).click()
  await expect(page.getByText('Healthy', { exact: true })).toBeVisible({ timeout: 35_000 })
  await expect(page.getByText(/http:\/\/127\.0\.0\.1:/)).toBeVisible()

  await page.getByRole('tab', { name: 'Changes' }).click()
  await page.getByRole('button', { name: 'Restore this version' }).click()
  await expect(page.getByRole('status')).toContainText('restored transactionally')

  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+K' : 'Control+K')
  await page.getByRole('button', { name: 'Review saved knowledge' }).click()
  await expect(page.getByRole('heading', { name: 'What Ion knows' })).toBeVisible()
  await page.goBack()
  await expect(page.getByRole('heading', { name: 'Welcome project' })).toBeVisible()
  await expect(page.getByTestId('persistent-chat-host')).toBeVisible()
})
