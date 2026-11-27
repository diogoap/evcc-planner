import { expect, test } from '@playwright/test'

// Demo mode at the fixed clock (Monday 19:00). A trip to School at 19:30
// leaves home at 19:03, so with 15 minutes ahead car heating's time has
// come. The demo car is unplugged, so it asks on the phone first; the demo
// phone's buttons are pressed in Settings → Notifications.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

test('turn on car heating, confirm it on the phone, and see it on the Overview', async ({ page }) => {
  const notify = await (await page.request.get('/api/settings/notifications')).json()
  const on = { ...notify, enabled: true, phones: ['mobile_app_demo_phone'], questions: false }
  expect((await page.request.put('/api/settings/notifications', { data: on })).status()).toBe(200)

  await page.goto('/settings')
  await page.getByRole('link', { name: 'Climate', exact: true }).click()
  const form = page.getByRole('region', { name: 'Car heating' })
  await expect(page.getByTestId('heating-climateEntity')).toHaveValue('climate.demo_car')
  await expect(page.getByTestId('heating-outside')).toContainText('2 °C')
  await expect(form).toContainText('heat_cool at 20 °C')
  await page.getByTestId('heating-enabled').click()
  await page.getByRole('button', { name: 'Save car heating settings' }).click()
  await expect(page.getByText('Car heating settings saved')).toBeVisible()

  const trip = {
    title: 'Evening class',
    destination: 'School',
    date: '2026-11-09',
    start: '19:30',
    end: '20:30',
  }
  expect((await page.request.post('/api/trips', { data: trip })).status()).toBe(201)
  // The demo control checks right away (the job also does, every minute).
  await page.getByTestId('heating-demo-outside').fill('1')
  await page.getByTestId('heating-demo-set').click()
  await expect(page.getByTestId('heating-recent')).toContainText('Asked on the phone at 19:00')

  await page.getByRole('link', { name: 'Notifications', exact: true }).click()
  const question = page.getByTestId('notification-heat_ask')
  await expect(question).toContainText('[EVCC Planner] ❄️ Heat the car?')
  await expect(question).toContainText('Evening class leaves at 19:03. Heat it to 20 °C?')
  await question.getByRole('button', { name: 'Heat now' }).click()
  await expect(page.getByText('Heat now pressed')).toBeVisible()
  await expect(question).toContainText('Heating turned on')
  // Car heating on (Settings → Notifications) says so on the phone.
  await expect(page.getByTestId('notification-heat_on')).toContainText('[EVCC Planner] 🔥 Car heating on')
  await expect(page.getByTestId('notification-heat_on')).toContainText('Heating the car to 20 °C')

  await page.goto('/')
  await expect(page.getByTestId('heating')).toContainText('On since 19:00, after Heat now')
  await expect(page.getByTestId('heating')).toContainText('10 °C in the car')
  // The car's climate from Home Assistant, next to Home and Unplugged.
  await expect(page.getByTestId('climate')).toHaveText('Heating')
  await page.goto('/activity')
  await expect(page.locator('li.activity').filter({ hasText: 'Heat now pressed' })).toHaveCount(1)
  await expect(
    page.locator('li.activity').filter({ hasText: 'Turned on the car heating for Evening class' }),
  ).toHaveCount(1)
})
