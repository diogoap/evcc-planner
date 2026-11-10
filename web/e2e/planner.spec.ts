import { expect, test, type Page } from '@playwright/test'

// Demo data at the fixed clock (Monday 9 November 2026, 19:00 Amsterdam):
// school runs on weekdays (Wednesday's drop-off skipped), an office visit on
// Tuesday, and an unplugged car last read at 38%.

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

async function shot(page: Page, name: string) {
  await page.screenshot({ path: test.info().outputPath(`${name}.png`), fullPage: true })
}

test('overview shows status, the next departure, and seven days', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByTestId('soc')).toHaveText('38%')
  await expect(page.getByTestId('soc-range')).toHaveText('122 km range')
  await expect(page.getByTestId('soc-stale')).toContainText('Last known, Mon 9 Nov, 16:45')
  await expect(page.getByTestId('next-departure')).toHaveText('Tomorrow, leave 07:48')
  await expect(page.getByTestId('next-target')).toHaveText('42%')
  const tue = page.getByTestId('day-2026-11-10')
  await expect(tue).toContainText('100 km')
  await expect(tue).toContainText('18.0 kWh')
  await expect(tue).toContainText('42% by 07:48')
  await expect(page.getByTestId('day-2026-11-14')).toContainText('1 to review')
  await page.goto('/trips#day-2026-11-10')
  await expect(page.getByTestId('trip-recurring:1:2026-11-10').getByTestId('departure-target')).toHaveText(
    'Needs 42% at departure',
  )
  await shot(page, 'overview')
})

test('add a manual trip and keep it after reload', async ({ page }) => {
  await page.goto('/trips')
  await page.getByRole('button', { name: '+ Add trip' }).click()
  const dialog = page.getByRole('dialog', { name: 'Add trip' })
  await dialog.getByLabel('Destination').fill('Bouwmarkt 5, Demo Town')
  await dialog.getByLabel('Title').fill('Hardware store')
  await dialog.getByLabel('Date').fill('2026-11-15')
  await dialog.getByLabel('Arrive by').fill('10:00')
  await dialog.getByRole('button', { name: 'Save trip' }).click()
  await expect(dialog).toBeHidden()

  const sunday = page.locator('#day-2026-11-15')
  await expect(sunday).toContainText('Hardware store')
  await expect(sunday).toContainText('until 10:15')
  await page.reload()
  await expect(page.locator('#day-2026-11-15')).toContainText('Hardware store')
  await page.goto('/')
  await expect(page.getByTestId('day-2026-11-15')).not.toContainText('No trips')
})

test('disable one date of a recurring trip', async ({ page }) => {
  await page.goto('/trips#day-2026-11-12')
  const row = page.getByTestId('trip-recurring:2:2026-11-12')
  await row.getByRole('switch').click()
  await expect(row.getByRole('switch')).toHaveAttribute('aria-checked', 'false')
  await page.reload()
  await expect(page.getByTestId('trip-recurring:2:2026-11-12').getByRole('switch')).toHaveAttribute(
    'aria-checked',
    'false',
  )

  await page.goto('/recurring')
  const pickup = page.getByTestId('template-2')
  await expect(pickup.getByRole('button', { name: /Thu 12/ })).toHaveAttribute('aria-pressed', 'false')
  await expect(pickup.getByRole('button', { name: /Fri 13/ })).toHaveAttribute('aria-pressed', 'true')
  await shot(page, 'recurring')
})

test('schedule edits keep per-date changes', async ({ page }) => {
  // Move Friday's drop-off on the Trips page.
  await page.goto('/trips#day-2026-11-13')
  await page.getByTestId('trip-recurring:1:2026-11-13').getByRole('button', { name: 'Edit' }).click()
  const edit = page.getByRole('dialog', { name: 'Edit trip' })
  await edit.getByLabel('Arrive by').fill('08:45')
  await edit.getByRole('button', { name: 'Save this date' }).click()
  await expect(edit).toBeHidden()

  // Change the schedule's time.
  await page.goto('/recurring')
  await page.getByTestId('template-1').getByRole('button', { name: 'Edit schedule' }).click()
  const sched = page.getByRole('dialog', { name: 'Edit schedule' })
  await sched.getByLabel('Arrive by').fill('08:10')
  await sched.getByRole('button', { name: 'Save schedule' }).click()
  await expect(sched).toBeHidden()

  await page.goto('/trips')
  await expect(page.getByTestId('trip-recurring:1:2026-11-12')).toContainText('08:10')
  await expect(page.getByTestId('trip-recurring:1:2026-11-13')).toContainText('08:45')
  await expect(page.getByTestId('trip-recurring:1:2026-11-13')).toContainText('Edited')
})

test('confirm an unclear calendar trip', async ({ page }) => {
  await page.goto('/trips#day-2026-11-14')
  const day = page.locator('#day-2026-11-14')
  await expect(day).toContainText('Needs review')
  await day.getByRole('button', { name: 'Confirm car trip' }).click()
  await expect(day).not.toContainText('Needs review')
  await expect(page.getByText('Calendar events not counted as trips (1)')).toBeVisible()
})

test('calendar events without a location are shown disabled', async ({ page }) => {
  await page.goto('/trips#day-2026-11-13')
  const friday = page.locator('#day-2026-11-13')
  const piano = friday.locator('li', { hasText: 'Piano lesson' })
  await expect(piano.getByRole('switch')).toHaveAttribute('aria-checked', 'false')
  await expect(piano.getByRole('switch')).toBeDisabled()
  await expect(piano.getByTestId('disabled-reason')).toHaveText('No location')
  await expect(page.getByText('Some trips have no distance')).toHaveCount(0)
})

test('settings persist and change the plan', async ({ page }) => {
  await page.goto('/settings')
  await expect(page.getByTestId('home-address')).toHaveValue('Demo Home 1, Demo Town')
  await expect(page.getByTestId('setting-chargeBufferMin')).toHaveAccessibleDescription(
    /Charging must be done this long before each departure/,
  )
  await page.getByTestId('setting-consumptionKWhPer100Km').fill('20')
  await page.getByRole('button', { name: 'Save settings' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  await page.reload()
  await expect(page.getByTestId('setting-consumptionKWhPer100Km')).toHaveValue('20')
  await page.goto('/')
  await expect(page.getByTestId('day-2026-11-10')).toContainText('20.0 kWh')

  await page.goto('/settings')
  await page.getByRole('link', { name: 'Places' }).click()
  await page.getByLabel('New place name').fill('Gym')
  await page.getByLabel('New place address').fill('Sportlaan 9, Demo Town')
  await page.getByRole('button', { name: 'Add place' }).click()
  await expect(page.getByText('Sportlaan 9, Demo Town')).toBeVisible()
  await shot(page, 'settings-places')
})

test('send a plan to EVCC only after confirmation', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'Send plan to EVCC' }).click()
  let dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('once the car is plugged in')
  await expect(page.getByTestId('current-plan')).toHaveText('None')
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(page.getByTestId('evcc-plan')).toHaveText('None')

  // Plug in the demo car so EVCC can preview.
  await page.goto('/settings')
  await page.getByRole('link', { name: 'Connections' }).click()
  await page.getByLabel('Plugged in').check()
  await page.getByRole('button', { name: 'Apply' }).click()
  await expect(page.getByText('Demo car plugged in')).toBeVisible()

  await page.goto('/')
  await page.getByRole('button', { name: 'Send plan to EVCC' }).click()
  dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('EVCC plans')
  await expect(page.getByTestId('plan-soc')).toHaveValue('42')
  await shot(page, 'send-plan')
  await dialog.getByRole('button', { name: 'Confirm and send' }).click()
  await expect(page.getByText('Plan sent to EVCC: 42% by Tue 10 Nov, 07:48')).toBeVisible()
  await expect(page.getByTestId('evcc-plan')).toContainText('42% by Tue 10 Nov, 07:48')
  await expect(page.getByTestId('evcc-plan')).toContainText('Set by planner')
})

test('installable as an app: manifest and icons', async ({ page, request }) => {
  await page.goto('/')
  await expect(page.locator('link[rel="manifest"]')).toHaveAttribute('href', '/manifest.webmanifest')
  await expect(page.locator('link[rel="apple-touch-icon"]')).toHaveAttribute('href', '/apple-touch-icon.png')
  const res = await request.get('/manifest.webmanifest')
  expect(res.headers()['content-type']).toContain('application/manifest+json')
  const manifest = await res.json()
  expect(manifest).toMatchObject({ name: 'EVCC Trip Planner', start_url: '/', display: 'standalone' })
  const sizes = manifest.icons.map((i: { sizes: string }) => i.sizes)
  expect(sizes).toEqual(expect.arrayContaining(['192x192', '512x512']))
  expect(manifest.icons.some((i: { purpose: string }) => i.purpose === 'maskable')).toBe(true)
  for (const src of [...manifest.icons.map((i: { src: string }) => i.src), '/apple-touch-icon.png']) {
    const icon = await request.get(src)
    expect(icon.status(), src).toBe(200)
    expect(icon.headers()['content-type'], src).toMatch(/^image\//)
  }
})

test('every page fits the viewport without errors', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
  for (const path of ['/', '/trips', '/recurring', '/settings']) {
    await page.goto(path)
    await page.waitForLoadState('networkidle')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
    expect(overflow, `${path} scrolls horizontally`).toBe(false)
  }
  expect(errors).toEqual([])
})
