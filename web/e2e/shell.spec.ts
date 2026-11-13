import { expect, test } from '@playwright/test'

// The app shell: navigation, refresh, and dialogs at desktop and phone size.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

test('main navigation: tabs on wide screens, a bar at the bottom on phones', async ({ page, isMobile }) => {
  await page.goto('/')
  const bottom = page.getByTestId('bottom-nav')
  const top = page.locator('.topbar').getByRole('navigation', { name: 'Main' })
  if (!isMobile) {
    await expect(top).toBeVisible()
    await expect(bottom).toBeHidden()
    await expect(top.getByRole('link')).toHaveText([
      'Overview',
      'Trips',
      'Recurring trips',
      'Activity',
      'Settings',
    ])
    return
  }
  await expect(top).toBeHidden()
  await expect(bottom.getByRole('link')).toHaveText([
    'Overview',
    'Trips',
    'Recurring',
    'Activity',
    'Settings',
  ])
  await bottom.getByRole('link', { name: 'Trips' }).click()
  await expect(page).toHaveURL(/\/trips$/)
  await expect(bottom.getByRole('link', { name: 'Trips' })).toHaveAttribute('aria-current', 'page')

  // The end of the page scrolls clear of the bar.
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
  const last = await page.locator('main > :last-child').boundingBox()
  const bar = await bottom.boundingBox()
  expect(last!.y + last!.height).toBeLessThanOrEqual(bar!.y)
})

test('refresh reloads the plan and shows when it was updated', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByTestId('updated')).toHaveText('Updated 19:00')
  const reloaded = page.waitForResponse((r) => r.url().endsWith('/api/plan'))
  await page.getByRole('button', { name: 'Refresh' }).click()
  await reloaded
  await expect(page.getByRole('button', { name: 'Refresh' })).toBeEnabled()
  await expect(page.getByTestId('updated')).toHaveText('Updated 19:00')
})

test('dialogs open as sheets from the bottom on phones and lock the page', async ({ page, isMobile }) => {
  await page.goto('/trips')
  await page.getByRole('button', { name: 'Add trip' }).click()
  const dialog = page.getByRole('dialog', { name: 'Add trip' })
  await expect(dialog).toBeVisible()
  await dialog.evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished)))
  await expect(page.locator('html')).toHaveClass(/modal-open/)

  const box = (await dialog.boundingBox())!
  const screen = page.viewportSize()!
  if (isMobile) {
    expect(box.x).toBe(0)
    expect(box.width).toBe(screen.width)
    expect(Math.round(box.y + box.height)).toBe(screen.height)
  } else {
    expect(box.width).toBeLessThan(screen.width / 2)
  }

  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(page.locator('html')).not.toHaveClass(/modal-open/)
})
