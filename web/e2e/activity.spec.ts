import { expect, test, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'

// The activity log starts empty after a demo reset; demo integrations are
// stubs, so no external calls are recorded.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

async function shot(page: Page, name: string) {
  await page.screenshot({ path: test.info().outputPath(`${name}.png`), fullPage: true })
}

test('a trip change appears in its history with before and after', async ({ page }) => {
  await page.goto('/trips#day-2026-11-12')
  const row = page.getByTestId('trip-recurring:2:2026-11-12')
  await row.getByRole('switch').click()
  await expect(row.getByRole('switch')).toHaveAttribute('aria-checked', 'false')

  await row.getByRole('button', { name: 'History of School pickup on 2026-11-12' }).click()
  await expect(page).toHaveURL(/\/activity\?subject=trip/)
  await expect(page.getByTestId('subject-filter')).toContainText('History of School pickup, Thu 12')
  const item = page.locator('li.activity').first()
  await expect(item).toContainText('Disabled School pickup on Thu 12 Nov')
  await expect(item.getByTestId('event-changes')).toContainText('Yes → No')
  await expect(item).toContainText('UI')

  // The schedule's history includes its dates' changes.
  await page.goto('/recurring')
  await page.getByTestId('template-2').getByRole('link', { name: 'History' }).click()
  await expect(page.getByTestId('subject-filter')).toContainText('History of School pickup')
  await expect(page.locator('li.activity')).toHaveCount(1)
  await page.getByRole('button', { name: 'Show all' }).click()
  await expect(page.getByTestId('subject-filter')).toBeHidden()
  await shot(page, 'activity-history')
})

test('filter, export, and see external API usage', async ({ page, request }) => {
  await request.post('/api/places', { data: { name: 'Pool', address: 'Badweg 1, Demo Town' } })
  await request.patch('/api/trips/recurring:1:2026-11-13', { data: { start: '08:45' } })
  await request.post('/api/evcc/plan', {
    data: { soc: 42, time: '2026-11-10T06:48:00Z', tripKey: 'recurring:1:2026-11-10', confirm: true },
  })

  await page.goto('/')
  await page.getByTestId('plan-history').click()
  await expect(page.getByTestId('subject-filter')).toContainText('History of the EVCC plan')
  const sent = page.locator('li.activity').first()
  await expect(sent).toContainText('Sent plan 42% by Tue 10 Nov, 07:48 to EVCC')
  await expect(sent.getByTestId('event-changes')).toContainText('none → 42% by 2026-11-10 07:48')

  await page.goto('/activity')
  await expect(page.locator('li.activity')).toHaveCount(3)
  await page.getByLabel('Type').selectOption('place')
  await expect(page).toHaveURL(/type=place/)
  await expect(page.locator('li.activity')).toHaveCount(1)
  await expect(page.locator('li.activity')).toContainText('Added place Pool')
  await page.getByLabel('Source').selectOption('job')
  await expect(page.getByTestId('activity-empty')).toContainText('No activity matches these filters')
  await page.getByRole('button', { name: 'Clear filters' }).click()
  await expect(page.locator('li.activity')).toHaveCount(3)

  const download = page.waitForEvent('download')
  await page.getByTestId('export-csv').click()
  const file = await download
  expect(file.suggestedFilename()).toBe('evcc-planner-activity-2026-11-09.csv')
  const csv = readFileSync(await file.path(), 'utf8')
  expect(csv.split('\n')[0]).toContain('id,time,type,source,action')
  expect(csv).toContain('Edited School drop-off on Fri 13 Nov: arrive-by time, end time')

  const routes = page.getByTestId('usage-google_routes')
  await expect(routes.getByTestId('free-calls')).toHaveText('0 of 5,000 free calls this month')
  await expect(routes).toContainText('Stub or not configured')
  await shot(page, 'activity')
})
