import { expect, test } from '@playwright/test'

// Connections that need attention: a demo outage shows on the Overview,
// links to Settings → Connections, and goes away once the service works.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

test('an outage shows on the Overview until the service works again', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByTestId('ready-headline')).toBeVisible()
  await expect(page.getByTestId('health-line')).toHaveCount(0)

  // Settings → Connections opens from a link; its demo card takes Home
  // Assistant down.
  await page.goto('/settings?tab=connections')
  const homeAssistant = page.getByTestId('connection-Home Assistant')
  await expect(homeAssistant).toContainText('Stub')
  await page.getByTestId('demo-fault-homeassistant').click()
  await expect(page.getByTestId('demo-fault-homeassistant')).toHaveAttribute('aria-checked', 'true')
  await expect(homeAssistant).toContainText('Error')
  await expect(homeAssistant).toContainText('connection refused (demo outage)')

  // The Overview says so, with when it last worked, and links back.
  await page.goto('/')
  const line = page.getByTestId('health-line')
  await expect(line.getByTestId('health-Home Assistant')).toHaveText(
    /^Home Assistant: connection refused \(demo outage\) · last worked \d\d:\d\d$/,
  )
  await line.getByRole('link', { name: 'Connections' }).click()
  await expect(page).toHaveURL(/\/settings\?tab=connections$/)
  await expect(homeAssistant).toContainText('Error')

  // Working again: the line goes.
  await page.getByTestId('demo-fault-homeassistant').click()
  await expect(homeAssistant).toContainText('Stub')
  await page.goto('/')
  await expect(page.getByTestId('ready-headline')).toBeVisible()
  await expect(page.getByTestId('health-line')).toHaveCount(0)
})
