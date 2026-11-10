import { expect, test, type Page } from '@playwright/test'

// Demo forecasts at the fixed clock (Monday 9 November 2026, 19:00): EVCC has
// published Monday and Tuesday; the demo outlook names Tuesday the cheapest
// and Thursday the dearest day; other days only have its weekly average.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

async function shot(page: Page, name: string) {
  await page.screenshot({ path: test.info().outputPath(`${name}.png`), fullPage: true })
}

test('daily advice per day, with a confirmed extra plan that can be ended', async ({ page }) => {
  await page.goto('/')
  const tue = page.getByTestId('advice-2026-11-10')
  await expect(tue.getByTestId('badge-advice')).toHaveText('Charge ahead')
  await expect(tue.getByTestId('badge-confidence')).toHaveText('2/3')
  await expect(tue.getByTestId('badge-price')).toHaveText('€0.22')
  // Sun: EVCC's local forecast today and tomorrow, KNMI's sunshine after.
  await expect(tue.getByTestId('badge-sun')).toHaveText(/^\d+\.\d kWh$/)
  const wed = page.getByTestId('advice-2026-11-11')
  await expect(wed.getByTestId('badge-advice')).toHaveText('No data')
  await expect(wed.getByTestId('badge-sun')).toHaveText(/^\d+%$/)
  await expect(wed.getByTestId('badge-price')).toHaveText('~€0.25')
  await expect(page.getByTestId('advice-2026-11-09').getByTestId('badge-advice')).toHaveText('Defer')

  await page.getByTestId('advice-2026-11-10').click()
  const card = page.getByTestId('advice-card')
  await expect(card).toContainText("Tuesday's advice")
  await expect(card.getByTestId('advice-confidence')).toHaveText('Confidence: 2/3 — Medium')
  await expect(card.getByTestId('advice-basis')).toHaveText('Published')
  await expect(card.getByTestId('advice-sun')).toContainText('while home (EVCC solar forecast, local)')
  await card.getByText('Why?').click()
  await expect(card.getByTestId('advice-sources')).toContainText('Tibber outlook')
  await shot(page, 'advice')

  // Opening the proposal writes nothing.
  await card.getByRole('button', { name: 'Create extra plan' }).click()
  let dialog = page.getByRole('dialog', { name: /Create extra plan — Tuesday 10 November/ })
  await expect(dialog.getByTestId('proposal-soc')).toHaveValue('80')
  await expect(dialog.getByTestId('proposal-protected')).toContainText('needs 42%')
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(page.getByTestId('evcc-plan')).toHaveText('None')

  await card.getByRole('button', { name: 'Create extra plan' }).click()
  dialog = page.getByRole('dialog', { name: /Create extra plan/ })
  await dialog.getByTestId('proposal-soc').fill('60')
  await shot(page, 'extra-plan')
  await dialog.getByRole('button', { name: 'Confirm and create plan' }).click()
  await expect(page.getByText('Extra plan sent to EVCC: 60% by Tue 10 Nov, 07:48')).toBeVisible()
  await expect(page.getByTestId('evcc-plan')).toContainText('60% by Tue 10 Nov, 07:48')
  await expect(page.getByTestId('active-adjustment')).toContainText('60% by Tue 10 Nov, 07:48')
  await expect(card.getByTestId('advice-adjustment')).toContainText('Extra plan active')

  await card.getByRole('button', { name: 'End now and restore' }).click()
  await expect(page.getByText('Ended; the previous EVCC setting is restored')).toBeVisible()
  await expect(page.getByTestId('evcc-plan')).toHaveText('None')
  await expect(page.getByTestId('active-adjustment')).toHaveCount(0)
})

test('dismissing advice changes nothing and can be undone', async ({ page }) => {
  await page.goto('/')
  await page.getByTestId('advice-2026-11-10').click()
  const card = page.getByTestId('advice-card')
  await card.getByRole('button', { name: 'Dismiss' }).click()
  await expect(card).toContainText('Dismissed; nothing was changed.')
  await expect(card.getByRole('button', { name: 'Create extra plan' })).toHaveCount(0)
  await expect(page.getByTestId('evcc-plan')).toHaveText('None')
  await card.getByRole('button', { name: 'Undo' }).click()
  await expect(card.getByRole('button', { name: 'Create extra plan' })).toBeVisible()
})

test('weak or missing advice offers no plan action', async ({ page }) => {
  await page.goto('/')
  await page.getByTestId('advice-2026-11-11').click()
  const card = page.getByTestId('advice-card')
  await expect(card.getByTestId('advice-label')).toHaveText('No data / no suggestion')
  await expect(card.getByTestId('advice-basis')).toHaveText('Weekly average')
  await expect(card.getByTestId('advice-sun')).toContainText('of possible sunshine (KNMI outlook, national)')
  await expect(
    card.getByRole('button', { name: /Create extra plan|Review postponement|Dismiss/ }),
  ).toHaveCount(0)
})

test('review a postponement of cheap-grid charging', async ({ page }) => {
  await page.goto('/')
  await page.getByTestId('advice-2026-11-09').click()
  const card = page.getByTestId('advice-card')
  await card.getByRole('button', { name: 'Review postponement' }).click()
  const dialog = page.getByRole('dialog', { name: /Review postponement/ })
  await expect(dialog).toContainText('€0.20/kWh → paused')
  await expect(dialog.getByTestId('proposal-until')).toHaveValue('00:00')
  await dialog.getByRole('button', { name: 'Confirm postponement' }).click()
  await expect(page.getByText('Cheap-grid charging paused until Tue 10 Nov, 00:00')).toBeVisible()
  await expect(page.getByTestId('active-adjustment')).toContainText('Cheap charging paused')
})

test('advice auto-apply is off by default and needs a policy', async ({ page }) => {
  await page.goto('/settings')
  await page.getByRole('link', { name: 'Advice' }).click()
  const auto = page.getByTestId('advice-auto')
  await expect(auto).toHaveAttribute('aria-checked', 'false')
  await expect(auto).toBeDisabled()
  await expect(page.getByTestId('advice-auto-hint')).toContainText('Set a target for each indicator')
  await expect(page.getByTestId('advice-enabled')).toHaveAttribute('aria-checked', 'true')

  await page.getByTestId('advice-targetLittle').fill('60')
  await page.getByTestId('advice-targetMedium').fill('70')
  await page.getByTestId('advice-targetHigh').fill('80')
  await page.getByTestId('advice-readyBy').fill('06:00')
  await page.getByTestId('advice-expiryHours').fill('2')
  await expect(auto).toBeEnabled()
  await auto.click()
  await page.getByRole('button', { name: 'Save advice settings' }).click()
  await expect(page.getByText('Advice settings saved')).toBeVisible()
  await shot(page, 'settings-advice')

  await page.reload()
  await page.getByRole('link', { name: 'Advice' }).click()
  await expect(page.getByTestId('advice-auto')).toHaveAttribute('aria-checked', 'true')
  await page.goto('/')
  await expect(page.getByTestId('auto-apply')).toHaveText('On')

  // Turning daily advice off hides it.
  await page.goto('/settings')
  await page.getByRole('link', { name: 'Advice' }).click()
  await page.getByTestId('advice-enabled').click()
  await page.getByRole('button', { name: 'Save advice settings' }).click()
  await expect(page.getByText('Advice settings saved')).toBeVisible()
  await page.goto('/')
  await expect(page.getByTestId('advice-card')).toHaveCount(0)
  await expect(page.getByTestId('advice-2026-11-10')).toHaveCount(0)
})

test('connections show the forecast sources and price provenance', async ({ page }) => {
  await page.goto('/settings')
  await page.getByRole('link', { name: 'Connections' }).click()
  await expect(page.getByTestId('connection-Tibber outlook')).toContainText('Demo article')
  await expect(page.getByTestId('connection-OpenRouter')).toContainText('Pattern-based stub')
  await expect(page.getByTestId('connection-KNMI')).toContainText('Demo weather')
  const details = page.getByTestId('forecast-details')
  await expect(details).toContainText('Report Mon 09 – Sun 15')
  await expect(details).toContainText('converted')
  await expect(details).toContainText('Goedkoopste dag: Dinsdag')
  await page.getByRole('button', { name: 'Refresh forecasts' }).click()
  await expect(page.getByText('Forecasts refreshed')).toBeVisible()
  await shot(page, 'settings-connections')
})
