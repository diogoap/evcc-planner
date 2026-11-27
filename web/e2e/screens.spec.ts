import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'

// Every key screen at desktop and phone size (the two projects), in light and
// dark: no sideways scrolling, touch targets of at least 44 px on phones, no
// serious or critical accessibility findings, no page errors, and a
// screenshot of each for review (in test-results; not compared).

test.beforeEach(async ({ request }) => {
  const res = await request.post('/api/demo/reset', { data: {} })
  expect(res.status()).toBe(204)
})

interface Screen {
  name: string
  open: (page: Page) => Promise<unknown>
}

// Turns on AI mode and makes an AI plan (the demo's stand-in model).
async function aiMode(page: Page) {
  const settings = await (await page.request.get('/api/settings')).json()
  expect((await page.request.put('/api/settings', { data: { ...settings, tripPlans: 'ai' } })).status()).toBe(
    200,
  )
  expect((await page.request.post('/api/ai/run', { data: {} })).status()).toBe(200)
}

const settingsTab = (tab: string) => async (page: Page) => {
  await page.goto('/settings')
  await page.getByRole('link', { name: tab, exact: true }).click()
}

const screens: Screen[] = [
  { name: 'overview', open: (page) => page.goto('/') },
  {
    name: 'overview-list',
    open: async (page) => {
      await page.goto('/')
      await page.getByTestId('week-view-list').click()
    },
  },
  {
    name: 'day-panel',
    open: async (page) => {
      await page.goto('/?day=2026-11-10')
      await expect(page.getByTestId('day-panel')).toBeVisible()
    },
  },
  {
    // An outage: the health line at the top of the Overview.
    name: 'overview-health',
    open: async (page) => {
      const res = await page.request.put('/api/demo/faults', {
        data: { service: 'homeassistant', fault: 'down' },
      })
      expect(res.status()).toBe(200)
      await page.goto('/')
      await expect(page.getByTestId('health-line')).toBeVisible()
    },
  },
  { name: 'charts', open: (page) => page.goto('/charts') },
  { name: 'trips', open: (page) => page.goto('/trips') },
  {
    // The route of a manual trip, with a stop being added.
    name: 'trip-editor',
    open: async (page) => {
      // No #day: the same address again wouldn't reload the page.
      await page.goto('/trips')
      await page.getByTestId('trip-manual:1').getByRole('button', { name: 'Edit' }).click()
      const dialog = page.getByRole('dialog', { name: 'Edit trip' })
      await dialog.getByRole('button', { name: 'Add stop' }).click()
      await expect(dialog.getByLabel('Stop 2', { exact: true })).toBeFocused()
    },
  },
  { name: 'recurring', open: (page) => page.goto('/recurring') },
  { name: 'settings-home', open: settingsTab('Home & vehicle') },
  { name: 'settings-planning', open: settingsTab('Planning') },
  { name: 'settings-places', open: settingsTab('Places') },
  { name: 'settings-advice', open: settingsTab('Advice') },
  // Greyed out: AI mode is off in the demo.
  {
    name: 'settings-ai-off',
    open: async (page) => {
      await settingsTab('Advice')(page)
      await page.getByTestId('advice-tab-ai').click()
    },
  },
  {
    // AI mode with a plan from the demo's stand-in model.
    name: 'settings-ai',
    open: async (page) => {
      await aiMode(page)
      await settingsTab('Advice')(page)
      await expect(page.getByTestId('ai-summary')).toBeVisible()
    },
  },
  {
    name: 'overview-ai',
    open: async (page) => {
      await aiMode(page)
      await page.goto('/')
      await expect(page.getByTestId('ai-plan')).toBeVisible()
    },
  },
  { name: 'settings-notifications', open: settingsTab('Notifications') },
  {
    name: 'notification-copy',
    open: async (page) => {
      await settingsTab('Notifications')(page)
      await page.getByRole('button', { name: 'What back home reminders say' }).click()
      await expect(
        page.getByTestId('notification-copy').filter({ hasText: 'Back home: plug in' }),
      ).toBeVisible()
    },
  },
  {
    name: 'settings-heating',
    open: async (page) => {
      const heating = await (await page.request.get('/api/settings/heating')).json()
      expect(
        (await page.request.put('/api/settings/heating', { data: { ...heating, enabled: true } })).status(),
      ).toBe(200)
      await settingsTab('Heating')(page)
      await expect(page.getByTestId('heating-now')).toBeVisible()
    },
  },
  { name: 'settings-connections', open: settingsTab('Connections') },
  { name: 'activity', open: (page) => page.goto('/activity') },
  {
    name: 'send-dialog',
    open: async (page) => {
      await page.goto('/')
      await page.getByRole('button', { name: 'Send plan to EVCC' }).click()
      await expect(page.getByRole('dialog')).toBeVisible()
    },
  },
]

// Interactive elements a finger has to hit. A checkbox or radio counts with
// its label; links inside a sentence are exempt (WCAG 2.5.5).
async function smallTargets(page: Page) {
  return page.evaluate(() => {
    const min = 44
    const selector =
      'button, a[href], input:not([type=hidden]), select, textarea, summary, [role=switch], [tabindex="0"]'
    const small: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>(selector)) {
      if (el.closest('.sr-only, [inert]')) continue
      const target = (el instanceof HTMLInputElement && el.closest('label')) || el
      if (target.tagName === 'A' && getComputedStyle(target).display === 'inline') continue
      const r = target.getBoundingClientRect()
      if (r.width === 0 || r.height === 0) continue
      if (r.width < min || r.height < min) {
        const name = (target.getAttribute('aria-label') || target.textContent || '').trim().slice(0, 40)
        small.push(`${target.tagName.toLowerCase()} "${name}" ${Math.round(r.width)}×${Math.round(r.height)}`)
      }
    }
    return small
  })
}

for (const screen of screens) {
  test(`${screen.name}: layout and accessibility in light and dark`, async ({ page, isMobile }) => {
    const errors: string[] = []
    page.on('pageerror', (e) => errors.push(e.message))
    page.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
    for (const scheme of ['light', 'dark'] as const) {
      await test.step(scheme, async () => {
        await page.emulateMedia({ colorScheme: scheme })
        await screen.open(page)
        await page.waitForLoadState('networkidle')

        const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
        expect.soft(overflow, 'scrolls sideways').toBe(false)
        // An open dialog doesn't scroll sideways either.
        const sideways = await page.evaluate(() =>
          [...document.querySelectorAll<HTMLElement>('.modal')].some(
            (m) => m.scrollWidth > m.clientWidth && getComputedStyle(m).overflowX !== 'hidden',
          ),
        )
        expect.soft(sideways, 'a dialog scrolls sideways').toBe(false)
        if (isMobile) expect.soft(await smallTargets(page), 'touch targets under 44 px').toEqual([])

        const { violations } = await new AxeBuilder({ page }).analyze()
        const serious = violations
          .filter((v) => v.impact === 'serious' || v.impact === 'critical')
          .map((v) => `${v.id}: ${v.help} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)
        expect.soft(serious, 'serious accessibility findings').toEqual([])

        await page.screenshot({
          path: test.info().outputPath(`${screen.name}-${scheme}.png`),
          fullPage: true,
        })
      })
    }
    expect(errors).toEqual([])
  })
}
