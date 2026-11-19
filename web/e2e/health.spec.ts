import { expect, test } from '@playwright/test'

// Connections that need attention: a demo outage or a refused key shows on
// the Overview, links to Settings → Connections, and goes away once the
// service works.

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
  const fault = page.getByTestId('demo-fault-homeassistant')
  await expect(homeAssistant).toContainText('Stub')
  await fault.selectOption({ label: 'Down' })
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

  // A refused token: the line says which variable to renew.
  await fault.selectOption({ label: 'Key rejected' })
  await expect(homeAssistant).toContainText('Rejected')
  await expect(homeAssistant.getByTestId('connection-fix')).toHaveText(
    "Set a new HA_TOKEN in the planner's environment and restart it.",
  )
  await page.goto('/')
  await expect(line.getByTestId('health-Home Assistant')).toContainText(
    'access token rejected (HTTP 401): refused by the demo · last worked',
  )
  await expect(line.getByTestId('health-Home Assistant')).toContainText(
    "Set a new HA_TOKEN in the planner's environment and restart it.",
  )

  // Working again: the line goes.
  await page.goto('/settings?tab=connections')
  await fault.selectOption({ label: 'Works' })
  await expect(homeAssistant).toContainText('Stub')
  await page.goto('/')
  await expect(page.getByTestId('ready-headline')).toBeVisible()
  await expect(page.getByTestId('health-line')).toHaveCount(0)
})
