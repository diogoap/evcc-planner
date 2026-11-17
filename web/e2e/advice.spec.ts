import { expect, test, type Page } from '@playwright/test'

// Demo forecasts at the fixed clock (Monday 9 November 2026, 19:00): EVCC has
// published Monday and Tuesday; the demo outlook names Tuesday the cheapest
// and Thursday the dearest day; other days only have its weekly average.
// A past week of prices (lowest €0.40, seeded for the tests) is what
// Tuesday's charge extra compares with, as Wednesday isn't published yet.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
  const history = await request.post('/api/demo/history', { data: { low: 0.4 } })
  expect(history.status()).toBe(204)
})

async function shot(page: Page, name: string) {
  await page.screenshot({ path: test.info().outputPath(`${name}.png`), fullPage: true })
}

test('daily advice per day, with a confirmed extra plan that can be ended', async ({ page }) => {
  await page.goto('/')
  // The week strip: each day's advice and price.
  const tue = page.getByTestId('day-2026-11-10')
  await expect(tue.getByTestId('badge-advice')).toHaveText('Charge extra')
  // Published days show their lowest price, marked by a down arrow.
  await expect(tue.getByTestId('badge-price')).toHaveText(/^€0\.\d\d$/)
  await expect(tue.getByTestId('badge-price').getByRole('img', { name: 'Lowest' })).toBeAttached()
  const wed = page.getByTestId('day-2026-11-11')
  await expect(wed.getByTestId('badge-advice')).toHaveText('No data')
  await expect(wed.getByTestId('badge-price')).toHaveText('~€0.25')
  await expect(page.getByTestId('day-2026-11-09').getByTestId('badge-advice')).toHaveText('Defer')

  const overviewAdvice = page.getByTestId('advice-card').locator('#advice-title')
  await expect(overviewAdvice).toContainText("Monday's advice")
  await page.getByTestId('day-2026-11-10').click()
  const card = page.getByTestId('panel-advice')
  await expect(page.getByTestId('day-panel')).toContainText('Tuesday 10 November')
  // The Overview's advice card follows the day opened.
  await expect(overviewAdvice).toContainText("Tuesday's advice")
  await expect(card.getByTestId('advice-label')).toHaveText('Charge extra to 60%')
  await expect(card.getByTestId('advice-confidence')).toHaveText('Medium confidence')
  await expect(card.getByTestId('advice-indication')).toHaveText('Cheaper days ahead: medium')
  await expect(card.getByTestId('advice-reference')).toHaveText("Past 7 days' lowest €0.40")
  await expect(card.getByTestId('advice-basis')).toHaveText('Published')
  await expect(card.getByTestId('advice-sun')).toContainText('while home')
  await expect(card.getByTestId('advice-sun')).toContainText('EVCC')
  await card.getByText('Why?').click()
  await expect(card.getByTestId('advice-sources')).toContainText('Tibber outlook')
  await shot(page, 'advice')

  // Opening the proposal writes nothing.
  await card.getByRole('button', { name: 'Create extra plan' }).click()
  let dialog = page.getByRole('dialog', { name: /Create extra plan — Tuesday 10 November/ })
  await expect(dialog.getByTestId('proposal-soc')).toHaveValue('60')
  await expect(dialog.getByTestId('proposal-protected')).toContainText('needs 42%')
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(page.getByTestId('evcc-plan')).toHaveText('None')

  await card.getByRole('button', { name: 'Create extra plan' }).click()
  dialog = page.getByRole('dialog', { name: /Create extra plan/ })
  await dialog.getByTestId('proposal-soc').fill('70')
  await shot(page, 'extra-plan')
  await dialog.getByRole('button', { name: 'Confirm and create plan' }).click()
  await expect(page.getByText('Extra plan sent to EVCC: 70% by Tue 10 Nov, 05:45')).toBeVisible()
  await expect(page.getByTestId('evcc-plan')).toContainText('70% by Tue 10 Nov, 05:45')
  await expect(page.getByTestId('active-adjustment')).toContainText('70% by Tue 10 Nov, 05:45')
  await expect(card.getByTestId('advice-adjustment')).toContainText('Extra plan active')

  await card.getByRole('button', { name: 'End now and restore' }).click()
  await expect(page.getByText('Ended; the previous EVCC setting is restored')).toBeVisible()
  await expect(page.getByTestId('evcc-plan')).toHaveText('None')
  await expect(page.getByTestId('active-adjustment')).toHaveCount(0)
})

test('dismissing advice changes nothing and can be undone', async ({ page }) => {
  await page.goto('/')
  await page.getByTestId('day-2026-11-10').click()
  const card = page.getByTestId('panel-advice')
  await card.getByRole('button', { name: 'Dismiss' }).click()
  await expect(card).toContainText('Dismissed; nothing was changed.')
  await expect(card.getByRole('button', { name: 'Create extra plan' })).toHaveCount(0)
  await expect(page.getByTestId('evcc-plan')).toHaveText('None')
  await card.getByRole('button', { name: 'Undo' }).click()
  await expect(card.getByRole('button', { name: 'Create extra plan' })).toBeVisible()
})

test('weak or missing advice offers no plan action', async ({ page }) => {
  await page.goto('/')
  await page.getByTestId('day-2026-11-11').click()
  const card = page.getByTestId('panel-advice')
  await expect(card.getByTestId('advice-label')).toHaveText('No data / no suggestion')
  await expect(card.getByTestId('advice-basis')).toHaveText('Weekly average')
  await expect(card.getByTestId('advice-sun')).toContainText('of possible sunshine')
  await expect(card.getByTestId('advice-sun')).toContainText('KNMI')
  await expect(
    card.getByRole('button', { name: /Create extra plan|Review postponement|Dismiss/ }),
  ).toHaveCount(0)
})

test('review a postponement of cheap-grid charging', async ({ page }) => {
  await page.goto('/')
  await page.getByTestId('day-2026-11-09').click()
  const card = page.getByTestId('panel-advice')
  await card.getByRole('button', { name: 'Review postponement' }).click()
  const dialog = page.getByRole('dialog', { name: /Review postponement/ })
  await expect(dialog).toContainText('€0.25/kWh → paused')
  await expect(dialog.getByTestId('proposal-until')).toHaveValue('00:00')
  await dialog.getByRole('button', { name: 'Confirm postponement' }).click()
  await expect(page.getByText('Cheap-grid charging paused until Tue 10 Nov, 00:00')).toBeVisible()
  await expect(page.getByTestId('active-adjustment')).toContainText('Cheap charging paused')
})

test('charge extra steps per indication, and auto-apply off by default', async ({ page }) => {
  await page.goto('/settings')
  await page.getByRole('link', { name: 'Advice' }).click()
  // The steps for each indication of cheaper days ahead; high is off.
  await expect(page.getByTestId('extra-little-enabled')).toBeChecked()
  await expect(page.getByTestId('extra-little-0-discount')).toHaveValue('20')
  await expect(page.getByTestId('extra-little-2-target')).toHaveValue('100')
  await expect(page.getByTestId('extra-high-enabled')).not.toBeChecked()
  await page.getByTestId('extra-high-enabled').check()
  await page.getByTestId('extra-medium-0-target').fill('45')

  const auto = page.getByTestId('advice-auto')
  await expect(auto).toHaveAttribute('aria-checked', 'false')
  // Automatic plans need nothing else: they are ready when charge extra's
  // cheap hours end.
  await expect(auto).toBeEnabled()
  await expect(page.getByTestId('advice-auto-hint')).toContainText('ready when its cheap hours end')
  await expect(page.getByTestId('advice-enabled')).toHaveAttribute('aria-checked', 'true')
  await auto.click()
  await page.getByRole('button', { name: 'Save advice settings' }).click()
  await expect(page.getByText('Advice settings saved')).toBeVisible()
  await shot(page, 'settings-advice')

  await page.reload()
  await page.getByRole('link', { name: 'Advice' }).click()
  await expect(page.getByTestId('advice-auto')).toHaveAttribute('aria-checked', 'true')
  await expect(page.getByTestId('extra-high-enabled')).toBeChecked()
  await expect(page.getByTestId('extra-medium-0-target')).toHaveValue('45')
  // Each step needs a bigger discount than the one before; the server
  // refuses otherwise, and the saved steps stay.
  await page.getByTestId('extra-medium-1-discount').fill('30')
  await page.getByRole('button', { name: 'Save advice settings' }).click()
  await expect(page.getByRole('alert')).toContainText(
    'charge extra (medium): each step needs a bigger discount than the one before',
  )
  await page.getByRole('button', { name: 'Discard' }).click()
  await page.goto('/')
  await expect(page.getByTestId('auto-apply')).toHaveText('Plans created automatically')

  // Turning daily advice off hides it.
  await page.goto('/settings')
  await page.getByRole('link', { name: 'Advice' }).click()
  await page.getByTestId('advice-enabled').click()
  await page.getByRole('button', { name: 'Save advice settings' }).click()
  await expect(page.getByText('Advice settings saved')).toBeVisible()
  await page.goto('/')
  await expect(page.getByTestId('advice-card')).toHaveCount(0)
  await expect(page.getByTestId('day-2026-11-10').getByTestId('badge-advice')).toHaveCount(0)
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
