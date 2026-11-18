import { expect, test, type Page } from '@playwright/test'

// AI mode in demo mode: the stand-in model repeats the automatic rules, so
// the flow works without an OpenRouter key. Fixed clock: Monday 19:00.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

// Advice → AI mode.
async function openAI(page: Page) {
  await openSettings(page, 'Advice')
  await page.getByTestId('advice-tab-ai').click()
}

async function openSettings(page: Page, tab: string) {
  await page.goto('/settings')
  await page.getByRole('link', { name: tab, exact: true }).click()
}

test('choose AI mode, make an AI plan, and see it on the Overview', async ({ page }) => {
  await openSettings(page, 'Planning')
  await page.getByTestId('trip-plans-ai').check()
  await page.getByRole('button', { name: 'Save settings' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  await expect(page.getByTestId('planning-status')).toContainText('AI mode')
  await expect(page.getByTestId('planning-ai')).toContainText('No AI plan yet')
  // The automatic rules' advice doesn't apply in AI mode; AI mode has its own switch.
  await page.getByRole('link', { name: 'Advice', exact: true }).click()
  await expect(page.getByTestId('advice-tab-ai')).toHaveAttribute('aria-current', 'page')
  await page.getByTestId('advice-tab-rules').click()
  await expect(page.getByTestId('advice-ai-off')).toContainText('AI mode is on')
  await expect(page.getByTestId('advice-enabled')).toBeDisabled()
  await expect(page.getByTestId('advice-auto')).toBeDisabled()
  await page.getByRole('link', { name: 'Planning', exact: true }).click()

  await page.getByTestId('planning-ai').getByRole('link', { name: 'AI settings and the latest run' }).click()
  await expect(page.getByTestId('ai-decider')).toHaveText(
    'Stand-in (no AI model): repeats the automatic rules',
  )
  await expect(page.getByTestId('ai-auto-apply')).toBeEnabled()
  await page.getByTestId('ai-run-now').click()
  await expect(page.getByText('AI plan made and applied')).toBeVisible()
  await expect(page.getByTestId('ai-status')).toContainText('In use: the AI plan from Mon 9 Nov, 19:00.')
  await expect(page.getByTestId('ai-run-status')).toHaveText('OK')
  await expect(page.getByTestId('ai-summary')).toContainText('Stand-in decision')
  await expect(page.getByTestId('ai-days')).toContainText('Mon 09: Defer until Tue 10 Nov')
  // What was sent: the data, trips without titles.
  await page.getByText('What was sent and what came back').click()
  await expect(page.getByTestId('ai-sent')).toContainText(
    'T1 "School drop-off + Office visit": leaves 2026-11-10 07:48',
  )
  await expect(page.getByTestId('ai-sent')).not.toContainText('Schoolstraat')

  await page.goto('/')
  await expect(page.getByTestId('trip-plans')).toHaveText('AI mode')
  await expect(page.getByTestId('ai-plan')).toHaveText(/^Made Mon 9 Nov, 19:00\s*Run AI now$/)
  await expect(page.getByTestId('advice-ai')).toBeVisible()
  await page.getByTestId('ai-run-overview').click()
  await expect(page.getByText('AI plan made and applied')).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('overview-ai.png'), fullPage: true })

  // Every run is in the activity log.
  await page.goto('/activity')
  await expect(page.getByText('AI plan (run by hand)').first()).toBeVisible()
})

test('outside AI mode the AI tab is greyed out, but the prompt can be previewed', async ({ page }) => {
  await openAI(page)
  await expect(page.getByTestId('ai-off')).toContainText('AI mode is off')
  await expect(page.getByTestId('ai-run-now')).toBeDisabled()
  await expect(page.getByTestId('ai-max-runs')).toBeDisabled()
  await expect(page.getByTestId('ai-prompt')).not.toBeEditable()
  await page.getByRole('button', { name: 'Preview with today’s data' }).click()
  await expect(page.getByTestId('ai-preview')).toContainText(
    'T1 "School drop-off + Office visit": leaves 2026-11-10 07:48',
  )
  // Manual mode never acts on the advice by itself.
  await page.getByTestId('ai-off').getByRole('link', { name: 'Planning' }).click()
  await page.getByTestId('trip-plans-manual').check()
  await expect(page.getByTestId('setting-chargeAhead')).toBeDisabled()
  await page.getByRole('button', { name: 'Save settings' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  await page.getByRole('link', { name: 'Advice', exact: true }).click()
  await expect(page.getByTestId('advice-auto-manual')).toBeVisible()
  await expect(page.getByTestId('advice-auto')).toBeDisabled()
})

test('edit the prompt with placeholders, preview it, and go back to the default', async ({ page }) => {
  const settings = await (await page.request.get('/api/settings')).json()
  await page.request.put('/api/settings', { data: { ...settings, tripPlans: 'ai' } })
  await openAI(page)
  const prompt = page.getByTestId('ai-prompt')
  await expect(prompt).toHaveValue(/^You plan when and how much to charge/)
  await prompt.fill('Plan the week.\n{{prices}}')
  await expect(page.getByTestId('ai-unknown')).toContainText('Unknown placeholders: {{prices}}')
  await prompt.fill('Plan the week.\n')
  await page
    .getByTestId('ai-macros')
    .getByRole('button', { name: /^Insert \{\{trips\}\}/ })
    .click()
  await expect(prompt).toHaveValue('Plan the week.\n{{trips}}')
  await page.getByRole('button', { name: 'Preview with today’s data' }).click()
  await expect(page.getByTestId('ai-preview')).toContainText(
    'T1 "School drop-off + Office visit": leaves 2026-11-10 07:48',
  )
  await page.getByRole('button', { name: 'Save AI settings' }).click()
  await expect(page.getByText('AI settings saved')).toBeVisible()

  await page.reload()
  await page.getByRole('link', { name: 'Advice', exact: true }).click()
  await expect(prompt).toHaveValue('Plan the week.\n{{trips}}')
  page.once('dialog', (d) => d.accept())
  await page.getByTestId('ai-reset').click()
  await expect(prompt).toHaveValue(/^You plan when and how much to charge/)
  await page.getByRole('button', { name: 'Save AI settings' }).click()
  await expect(page.getByText('AI settings saved')).toBeVisible()
})

test('choose the AI model from the list, or type another', async ({ page }) => {
  const settings = await (await page.request.get('/api/settings')).json()
  await page.request.put('/api/settings', { data: { ...settings, tripPlans: 'ai' } })
  await openAI(page)
  const choice = page.getByTestId('ai-model-choice')
  await expect(choice).toHaveValue('~anthropic/claude-haiku-latest')
  await expect(choice.locator('option')).toHaveCount(20)
  await choice.selectOption('~openai/gpt-luna-latest')
  await page.getByRole('button', { name: 'Save AI settings' }).click()
  await expect(page.getByText('AI settings saved')).toBeVisible()
  await expect(page.getByTestId('ai-decider')).toContainText('Stand-in')

  await choice.selectOption({ label: 'Other model…' })
  const other = page.getByTestId('ai-model')
  await expect(other).toBeFocused()
  await other.fill('~mistral/devstral-latest')
  await page.getByRole('button', { name: 'Save AI settings' }).click()
  await expect(page.getByText('AI settings saved')).toBeVisible()
  await page.reload()
  await page.getByRole('link', { name: 'Advice', exact: true }).click()
  await expect(choice).toHaveValue('other')
  await expect(other).toHaveValue('~mistral/devstral-latest')
})
