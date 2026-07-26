import { expect, test } from '@playwright/test'

async function openPrimaryRoute(
  page: import('@playwright/test').Page,
  label: string,
) {
  const menu = page.getByRole('button', { name: 'Open navigation' })
  if (await menu.isVisible()) {
    if (label === 'Chat') {
      await page
        .getByRole('navigation', { name: 'Mobile navigation' })
        .getByRole('link', { name: 'Chat', exact: true })
        .click()
      return
    }
    await menu.click()
    const controls = page
      .locator('.shell-sidebar')
      .getByRole('button', { name: /Settings & control/ })
    if ((await controls.getAttribute('aria-expanded')) !== 'true') {
      await controls.click()
    }
    await page
      .locator('.shell-sidebar')
      .getByRole('link', { name: label, exact: true })
      .click()
    return
  }
  const link = page
    .locator('.shell-sidebar')
    .getByRole('link', { name: label, exact: true })
  const controls = page.getByRole('button', { name: /Settings & control/ })
  if (
    (await controls.isVisible()) &&
    (await controls.getAttribute('aria-expanded')) !== 'true'
  ) {
    await controls.click()
  }
  await link.click()
}

test('real encrypted turn, approval, navigation persistence, and audit journey', async ({
  page,
}, testInfo) => {
  await page.addInitScript(() => {
    const markReady = () => {
      const emptyConversation = document.querySelector('.empty-conversation')
      const composer = emptyConversation?.querySelector('.composer')
      const heading = emptyConversation?.querySelector('h2')
      const mode = emptyConversation?.querySelector('.composer-mode')
      if (
        composer !== null &&
        heading?.textContent?.includes('What can I do for you?') === true &&
        mode?.textContent?.includes('Full agent') === true
      ) {
        performance.mark('ion-startup-ready')
        observer.disconnect()
      }
    }
    const observer = new MutationObserver(markReady)
    observer.observe(document, { childList: true, subtree: true })
    markReady()
  })
  await page.goto('/chat')
  await expect(page.getByText('What can I do for you?')).toBeVisible()
  await expect(page.locator('.empty-conversation .composer')).toBeVisible()
  await expect(page.getByText('Full agent')).toBeVisible()
  const startupMilliseconds = await page.evaluate(
    () => performance.getEntriesByName('ion-startup-ready')[0]?.startTime,
  )
  expect(startupMilliseconds).toBeDefined()
  expect(startupMilliseconds).toBeLessThan(2_000)

  const computer = page.getByTestId('computer-stage')
  await expect(computer).toBeHidden()
  await expect(
    page.getByRole('separator', { name: 'Resize Computer stage' }),
  ).toHaveCount(0)

  const composer = page.getByLabel('Message Ion')
  await composer.fill('Publish the accepted release with evidence.')
  await page.getByRole('button', { name: 'Send' }).click()
  const approval = page.locator('.approval-card').last()
  await expect(approval.getByText('YOUR DECISION IS REQUIRED')).toBeVisible()
  await expect(
    approval.getByRole('heading', { name: 'Publish accepted release' }),
  ).toBeVisible()
  await approval.getByText('Review technical details').click()
  await expect(approval.getByText('Technical operation', { exact: true })).toBeVisible()
  await expect(approval.getByLabel('Redacted operation arguments')).toContainText(
    '[REDACTED]',
  )
  await expect(page.getByText('browser-must-not-see')).toHaveCount(0)

  await expect(computer).toBeHidden()
  await page.getByRole('button', { name: 'Review Computer' }).click()
  await expect(computer.getByText('Requested', { exact: true }).first()).toBeVisible()
  await expect(computer.getByText('Following live activity')).toBeVisible()
  await computer.getByRole('button', { name: 'Explore' }).click()
  await expect(
    computer.getByText(
      'Exploring is read-only. Use explicit conversation controls to steer, retry, approve, or take over.',
    ),
  ).toBeVisible()
  await computer.getByRole('button', { name: 'Watch' }).click()
  await computer.getByRole('button', { name: 'Open Computer full screen' }).click()
  await expect(computer).toHaveAttribute('data-fullscreen', 'true')
  await page.keyboard.press('Escape')
  await expect(computer).toHaveAttribute('data-fullscreen', 'false')
  const resizeHandle = page.getByRole('separator', { name: 'Resize Computer stage' })
  if (await resizeHandle.isVisible()) {
    const before = await resizeHandle.getAttribute('aria-valuenow')
    await resizeHandle.focus()
    await page.keyboard.press('ArrowLeft')
    await expect(resizeHandle).not.toHaveAttribute('aria-valuenow', before ?? '')
  }
  await computer.getByRole('button', { name: 'Pause view' }).click()
  await expect(computer.getByText('Viewing history while work continues')).toBeVisible()

  await openPrimaryRoute(page, 'Safety')
  await expect(page.getByRole('heading', { name: 'Decisions before consequences' })).toBeVisible()
  await openPrimaryRoute(page, 'Chat')
  await expect(
    approval.getByRole('heading', { name: 'Publish accepted release' }),
  ).toBeVisible()
  await expect(composer).toHaveValue('')

  await approval.getByRole('button', { name: 'Approve this action' }).click()
  await expect(
    page.getByText('The approved release was published with durable audit evidence.').last(),
  ).toBeVisible()
  await expect(computer).toBeHidden()
  await page.getByRole('button', { name: 'Review Computer' }).click()
  await expect(computer).toBeVisible()
  await expect(computer.getByText('Viewing history while work continues')).toBeVisible()
  await computer.getByRole('button', { name: 'Back to live' }).click()
  await expect(computer.getByText('Completed', { exact: true }).first()).toBeVisible()
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.reload()
  await expect(page.getByTestId('computer-stage')).toBeHidden()
  await page.getByRole('button', { name: 'Review Computer' }).click()
  await expect(page.getByTestId('computer-stage')).toBeVisible()
  await expect(
    page.getByTestId('computer-stage').getByText('Completed', { exact: true }).first(),
  ).toBeVisible()

  await openPrimaryRoute(page, 'Conversations')
  await expect(page.getByRole('heading', { name: 'Your conversations' })).toBeVisible()
  const savedConversation = page.locator('.conversation-open').filter({
    hasText: 'Publish the accepted release with evidence.',
  }).first()
  await expect(savedConversation).toBeVisible()
  await savedConversation.click()
  await expect(
    page.getByText('Publish the accepted release with evidence.').last(),
  ).toBeVisible()

  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+K' : 'Control+K')
  await expect(page.getByRole('dialog', { name: 'Search Ion' })).toBeVisible()
  await page.getByRole('button', { name: 'Review safety decisions' }).click()
  await expect(page.getByText('Your decision was recorded').first()).toBeVisible()
  await expect(page.getByText('A safety decision was recorded').first()).toBeVisible()

  const browserStorage = await page.evaluate(() => ({
    local: Object.keys(localStorage),
    session: Object.keys(sessionStorage),
    url: location.href,
  }))
  expect(browserStorage.local).toEqual([])
  expect(browserStorage.session).toEqual([])
  expect(browserStorage.url).not.toContain('ticket=')
  expect(browserStorage.url).not.toContain('token=')

  if (testInfo.project.name === 'chromium-mobile') {
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    )
    expect(overflow).toBeLessThanOrEqual(1)
  }
})

test('RED denial is durable and prevents the production effect', async ({ page }) => {
  await page.goto('/chat')
  const composer = page.getByLabel('Message Ion')
  await composer.fill('Attempt a release that must be denied.')
  await page.getByRole('button', { name: 'Send' }).click()
  const approval = page.locator('.approval-card').last()
  await expect(approval.getByText('YOUR DECISION IS REQUIRED')).toBeVisible()
  const successfulEffects = page.getByText(
    'The approved release was published with durable audit evidence.',
  )
  const successfulEffectsBeforeDenial = await successfulEffects.count()

  await approval.getByRole('button', { name: 'Deny' }).click()
  await expect(
    page.getByText('The operation was denied and was not published.').last(),
  ).toBeVisible()
  const deniedOutcome = page.locator('.activity-event').filter({
    hasText: 'An action was denied',
  }).last()
  await page.getByRole('button', { name: 'Activity' }).click()
  await deniedOutcome.getByText('Details', { exact: true }).click()
  await expect(deniedOutcome.getByText(/"terminal_status":"denied"/)).toBeVisible()
  await expect(successfulEffects).toHaveCount(successfulEffectsBeforeDenial)
  await page.getByRole('button', { name: 'Close work details' }).click()

  await openPrimaryRoute(page, 'Safety')
  const decision = page.locator('.event-row').filter({
    hasText: 'Your decision was recorded',
  }).first()
  await decision.getByText('Technical details').click()
  await expect(decision.getByText(/"decision":"deny"/)).toBeVisible()
  await expect(page.getByText('A safety decision was recorded').first()).toBeVisible()
})

test('real workspace tool renders the native Repository and Code application', async ({
  page,
}) => {
  await page.goto('/chat')
  const composer = page.getByLabel('Message Ion')
  await composer.fill('Inspect the real workspace in Computer.')
  await page.getByRole('button', { name: 'Send' }).click()

  const computer = page.getByTestId('computer-stage')
  await expect(
    page.getByText('The real workspace file was inspected and is visible in Computer.').last(),
  ).toBeVisible()
  await expect(computer).toBeHidden()
  await page.getByRole('button', { name: 'Review Computer' }).click()
  const application = computer.locator('[data-renderer="workspace"]')
  await expect(application).toBeVisible()
  await expect(application).toHaveAttribute('data-kind', 'code')
  await expect(application.getByRole('heading', {
    name: 'spec/ion_spec/spec.kvx',
  })).toBeVisible()
  await expect(application.getByLabel('Code output')).toContainText(
    'ION: the agent',
  )
  await expect(application.getByText(/Observed · Source/).first()).toBeVisible()
})

test('active turns can be steered and failed turns retried', async ({ page }) => {
  await page.goto('/chat')
  const composer = page.getByLabel('Message Ion')

  await composer.fill('Wait for steering before completing.')
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(page.getByRole('button', { name: 'Cancel turn' })).toBeEnabled()
  await composer.fill('Steering correction: return the bounded result.')
  await page.getByRole('button', { name: 'Steer active turn' }).click()
  await expect(page.getByText('Steering correction applied.').last()).toBeVisible()
  const cancelledTurn = page.locator('.conversation-callout').filter({
    hasText: 'The request could not be completed',
  }).last()
  await cancelledTurn.getByText('Technical details').click()
  await expect(cancelledTurn.getByText(/"error_class":"cancelled"/)).toBeVisible()

  await composer.fill('Fail once then retry this turn.')
  await page.getByRole('button', { name: 'Send' }).click()
  const failedOnce = page.locator('.conversation-callout').filter({
    hasText: 'The request could not be completed',
  }).last()
  await expect(failedOnce).toContainText(
    'some actions may already have completed',
  )
  const failedDetails = failedOnce.locator('details')
  await expect(failedDetails).toContainText(/"error_class":"permanent"/)
  await failedDetails.getByText('Technical details').click()
  await expect(failedDetails).toHaveAttribute('open', '')
  await expect(failedOnce.getByText(/"error_class":"permanent"/)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Retry last turn' })).toBeEnabled()
  await page.getByRole('button', { name: 'Retry last turn' }).click()
  await expect(page.getByText('Retry recovered the failed turn.').last()).toBeVisible()
})

test('automatic answer recovery does not surface an intermediate failure', async ({
  page,
}) => {
  await page.goto('/chat')
  const composer = page.getByLabel('Message Ion')
  await composer.fill('Recover an answer-validation checkpoint from durable evidence.')
  await page.getByRole('button', { name: 'Send' }).click()

  await expect(page.getByText(
    /The answer-validation checkpoint resumed from durable evidence/,
  ).last()).toBeVisible()
  await expect(page.getByText('The request could not be completed')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Retry last turn' })).toHaveCount(0)
})

test('delayed provider chunks render progressively in one assistant message', async ({
  page,
}) => {
  await page.goto('/chat')
  const composer = page.getByLabel('Message Ion')
  await composer.fill('Show a delayed streaming response.')
  await page.getByRole('button', { name: 'Send' }).click()

  const assistant = page.locator('.message.assistant').last()
  await expect(assistant).toContainText('Streaming arrives')
  await expect(assistant).not.toContainText('without duplicate messages')
  await expect(page.locator('.message.assistant')).toHaveCount(1)

  await expect(assistant).toContainText(
    'Streaming arrives in several visible steps without duplicate messages.',
  )
  await expect(page.locator('.message.assistant')).toHaveCount(1)
  await expect(assistant.getByText('Reasoning summary')).toHaveCount(0)
  await expect(assistant).not.toContainText('Checked progressive rendering.')
  await expect(page.getByText('Ion is working')).toHaveCount(0)
})
