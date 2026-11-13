import { expect, test } from '@playwright/test'

// Loading, empty, error, and unsaved states, on demo data at the fixed clock.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

test('settings show unsaved changes, can discard them, and ask before leaving', async ({
  page,
  isMobile,
}) => {
  await page.goto('/settings')
  const reserve = page.getByTestId('setting-arrivalReservePct')
  await expect(reserve).toHaveValue('15')
  if (isMobile) {
    // All six sections fit on screen as buttons.
    const width = page.viewportSize()!.width
    for (const link of await page
      .getByRole('navigation', { name: 'Settings sections' })
      .getByRole('link')
      .all()) {
      const box = (await link.boundingBox())!
      expect(box.x + box.width).toBeLessThanOrEqual(width)
    }
  }

  await reserve.fill('20')
  const bar = page.getByTestId('save-bar')
  await expect(bar).toContainText('Unsaved changes')
  await bar.getByRole('button', { name: 'Discard' }).click()
  await expect(reserve).toHaveValue('15')
  await expect(bar).not.toContainText('Unsaved changes')

  // Leaving with unsaved changes asks first; staying keeps them.
  await reserve.fill('20')
  page.once('dialog', (d) => d.dismiss())
  await page.getByRole('link', { name: 'Advice', exact: true }).click()
  await expect(reserve).toHaveValue('20')
  page.once('dialog', (d) => d.accept())
  await page.getByRole('link', { name: 'Advice', exact: true }).click()
  await expect(page.getByTestId('advice-enabled')).toBeVisible()
  await page.getByRole('link', { name: 'Home & vehicle', exact: true }).click()
  await expect(page.getByTestId('setting-arrivalReservePct')).toHaveValue('15')
})

test('a failed refresh keeps the data on screen, dated, with Retry', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByTestId('soc')).toHaveText('38%')
  await page.route('**/api/plan', (route) => route.abort())
  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByTestId('updated')).toHaveText(/Not updated 19:00/)
  await expect(page.getByTestId('soc')).toHaveText('38%')
  await expect(page.getByTestId('error-banner')).toHaveCount(0)

  await page.unroute('**/api/plan')
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByTestId('updated')).toHaveText('Updated 19:00')
})

test('the first load shows placeholders, then an error with Retry if it fails', async ({ page }) => {
  await page.route('**/api/plan', (route) => route.abort())
  await page.goto('/')
  const error = page.getByTestId('error-banner')
  await expect(error).toContainText("Couldn't load the plan")
  await page.unroute('**/api/plan')
  await error.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByTestId('soc')).toHaveText('38%')
})

test('empty places say what to do next', async ({ page, request }) => {
  for (const id of [1, 2]) expect((await request.delete(`/api/recurring/${id}`)).status()).toBeLessThan(300)
  await page.goto('/recurring')
  const empty = page.getByTestId('empty-state')
  await expect(empty).toContainText('No recurring trips yet')
  await empty.getByRole('button', { name: 'Add schedule' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
})
