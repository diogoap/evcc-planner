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

const settingsTab = (tab: string) => async (page: Page) => {
  await page.goto('/settings')
  await page.getByRole('link', { name: tab, exact: true }).click()
}

const screens: Screen[] = [
  { name: 'overview', open: (page) => page.goto('/') },
  { name: 'trips', open: (page) => page.goto('/trips') },
  { name: 'recurring', open: (page) => page.goto('/recurring') },
  { name: 'settings-home', open: settingsTab('Home & vehicle') },
  { name: 'settings-planning', open: settingsTab('Planning') },
  { name: 'settings-places', open: settingsTab('Places') },
  { name: 'settings-advice', open: settingsTab('Advice') },
  { name: 'settings-notifications', open: settingsTab('Notifications') },
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
