import { expect, test, type Page } from '@playwright/test'

// Demo mode at the fixed clock (Monday 19:00): the Home Assistant stub
// offers one phone and records what is sent; its buttons are pressed in
// Settings → Notifications.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

async function openNotifications(page: Page) {
  await page.goto('/settings')
  await page.getByRole('link', { name: 'Notifications' }).click()
}

test('turn on notifications, send a test, and answer a question from the phone', async ({ page }) => {
  await openNotifications(page)
  const form = page.getByRole('region', { name: 'Phone notifications' })
  await expect(form).toContainText('notify.mobile_app_demo_phone')
  await expect(page.getByTestId('notify-quiet-from')).toHaveValue('23:00')
  await expect(page.getByTestId('notify-quiet-to')).toHaveValue('07:00')
  await page.getByTestId('phone-mobile_app_demo_phone').check()
  await page.getByTestId('notify-enabled').click()
  await page.getByRole('button', { name: 'Save notification settings' }).click()
  await expect(page.getByText('Notification settings saved')).toBeVisible()

  await page.getByTestId('notify-test').click()
  await expect(page.getByText('Test notification sent')).toBeVisible()
  await expect(page.getByTestId('notification-test')).toContainText('EVCC Trip Planner')

  // The next EVCC check asks about the unclear calendar event.
  await page.getByRole('link', { name: 'Connections' }).click()
  await page.getByRole('button', { name: 'Apply' }).click()
  await expect(page.getByText('Demo car unplugged')).toBeVisible()
  await page.goto('/settings')
  await page.getByRole('link', { name: 'Connections' }).click()
  await expect(page.getByTestId('connection-Phone notifications')).toContainText('Demo phone')
  await page.getByRole('link', { name: 'Notifications' }).click()
  const question = page.getByTestId('notification-question')
  await expect(question).toContainText('Is this a car trip?')
  await expect(question).toContainText('"Birthday party" on Sat 14 Nov at 14:00')
  await page.screenshot({ path: test.info().outputPath('settings-notifications.png'), fullPage: true })
  await question.getByRole('button', { name: 'Count it' }).click()
  await expect(page.getByText('Count it pressed')).toBeVisible()
  await expect(question).toContainText('Counted as a car trip')

  // The trip no longer needs review, and the press is in the activity log.
  await page.goto('/')
  await expect(page.getByTestId('day-2026-11-14')).not.toContainText('to review')
  await page.goto('/activity')
  await expect(page.locator('li.activity').first()).toContainText('Count it pressed')
  await expect(page.locator('li.activity').first()).toContainText('Notification')
})
