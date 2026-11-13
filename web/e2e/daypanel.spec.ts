import { expect, test, type Page } from '@playwright/test'

// The day panel over the Overview: a sheet on phones, a side panel on
// desktop. Demo data at Monday 9 November 2026, 19:00.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

async function settled(page: Page) {
  await page
    .getByTestId('day-panel')
    .evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)))
}

test('a day opens in a panel with its trips, charging, advice, and actions', async ({ page, isMobile }) => {
  await page.goto('/')
  await page.getByTestId('day-2026-11-10').click()
  const panel = page.getByTestId('day-panel')
  await expect(page).toHaveURL(/\?day=2026-11-10$/)
  await expect(panel.getByRole('heading', { name: 'Tuesday 10 November' })).toBeFocused()
  await expect(panel.getByTestId('day-summary')).toContainText('100 km')
  await expect(panel.getByTestId('trip-recurring:1:2026-11-10')).toBeVisible()
  await expect(panel.getByTestId('day-charging')).toContainText('07:48 needs 42%')
  await expect(panel.getByTestId('day-windows')).toContainText('Expected')
  await expect(panel.getByTestId('panel-advice').getByTestId('advice-label')).toHaveText('Charge ahead')
  await expect(panel.getByTestId('day-send')).toHaveText('Send plan: Trips tomorrow')

  await settled(page)
  const box = (await panel.boundingBox())!
  const screen = page.viewportSize()!
  if (isMobile) {
    // A sheet from the bottom.
    expect(Math.round(box.y + box.height)).toBe(screen.height)
    expect(box.width).toBe(screen.width)
  } else {
    // A panel at the right edge.
    expect(Math.round(box.x + box.width)).toBe(screen.width)
    expect(box.height).toBe(screen.height)
  }
  await page.screenshot({ path: test.info().outputPath('day-panel.png') })

  // Trips switch off right here.
  await panel.getByRole('switch', { name: 'Disable School pickup on 2026-11-10' }).click()
  await expect(page.getByText('School pickup disabled for 2026-11-10')).toBeVisible()
  await expect(panel.getByRole('switch', { name: 'Enable School pickup on 2026-11-10' })).toBeVisible()

  // Adding a trip starts on this day; Escape closes only the dialog.
  await panel.getByTestId('day-add').click()
  const dialog = page.getByRole('dialog', { name: 'Add trip' })
  await expect(dialog.getByLabel('Date')).toHaveValue('2026-11-10')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(panel).toBeVisible()
})

test('move between days, then close with Escape or the back gesture', async ({ page }) => {
  await page.goto('/')
  await page.getByTestId('day-2026-11-10').click()
  const panel = page.getByTestId('day-panel')
  await panel.getByRole('button', { name: 'Next day' }).click()
  await expect(page).toHaveURL(/day=2026-11-11/)
  await expect(panel.getByRole('heading', { name: 'Wednesday 11 November' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(panel).toBeHidden()
  await expect(page).not.toHaveURL(/day=/)

  await page.getByTestId('day-2026-11-12').click()
  await expect(panel).toBeVisible()
  await page.goBack()
  await expect(panel).toBeHidden()
  await expect(page).toHaveURL(/\/$/)
})

test('tapping the chart opens that day', async ({ page, isMobile }) => {
  // Phones have the charts on their own page; closing the panel returns there.
  await page.goto(isMobile ? '/charts' : '/')
  const chart = page.getByTestId('chart-card').locator('.chart-price canvas')
  const box = (await chart.boundingBox())!
  // Halfway along the week.
  await chart.click({ position: { x: box.width / 2, y: box.height / 2 } })
  await expect(page.getByTestId('day-panel')).toBeVisible()
  await expect(page).toHaveURL(/day=2026-11-1[1-3]/)
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('day-panel')).toBeHidden()
  if (isMobile) await expect(page).toHaveURL(/\/charts$/)
})
