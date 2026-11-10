// Generates the app icons (SVG favicon, PWA and Apple touch PNGs) from one
// design: EVCC's green charging bolt inside a clock-like ring whose arc ends
// in an arrow (planning and automation). Run with `pnpm icons`.
import { chromium } from '@playwright/test'
import { writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const out = join(dirname(fileURLToPath(import.meta.url)), '..', 'public')

const green = '#0fdd42'
const C = 256 // centre of the 512 × 512 canvas

// Point on a circle, angle in degrees clockwise from 12 o'clock.
const at = (r, deg) => {
  const a = (deg * Math.PI) / 180
  return [C + r * Math.sin(a), C - r * Math.cos(a)]
}
const f = (n) => Math.round(n * 10) / 10

function artwork() {
  const r = 150
  const end = 300 // arc from 12 o'clock clockwise to 10 o'clock
  const [x0, y0] = at(r, 0)
  const [x1, y1] = at(r, end)
  // Arrowhead at the arc's end, pointing along the direction of travel.
  const rad = (end * Math.PI) / 180
  const tangent = [Math.cos(rad), Math.sin(rad)]
  const radial = [Math.sin(rad), -Math.cos(rad)]
  const tip = [x1 + tangent[0] * 62, y1 + tangent[1] * 62]
  const outer = [x1 + radial[0] * 44, y1 + radial[1] * 44]
  const inner = [x1 - radial[0] * 44, y1 - radial[1] * 44]
  const ticks = [90, 180, 270]
    .map((deg) => {
      const [a, b] = at(104, deg)
      const [c, d] = at(122, deg)
      return `<line x1="${f(a)}" y1="${f(b)}" x2="${f(c)}" y2="${f(d)}"/>`
    })
    .join('')
  return `
    <circle cx="${C}" cy="${C}" r="${r}" fill="none" stroke="#ffffff" stroke-opacity="0.12" stroke-width="34"/>
    <path d="M${f(x0)} ${f(y0)} A${r} ${r} 0 1 1 ${f(x1)} ${f(y1)}" fill="none" stroke="${green}" stroke-width="34" stroke-linecap="round"/>
    <path d="M${f(tip[0])} ${f(tip[1])} L${f(outer[0])} ${f(outer[1])} L${f(inner[0])} ${f(inner[1])} Z" fill="${green}"/>
    <g stroke="#ffffff" stroke-opacity="0.45" stroke-width="12" stroke-linecap="round">${ticks}</g>
    <path d="M18 4 8 18h7l-2 10 11-15h-7z" fill="#ffffff" transform="translate(${C} ${C}) scale(6.6) translate(-16 -16)"/>`
}

// rounded: transparent corners (browsers, Android "any"); otherwise a full
// square for platforms that apply their own mask. scale shrinks the artwork
// into the maskable safe zone.
function svg({ rounded, scale }) {
  const bg = rounded
    ? `<rect width="512" height="512" rx="112" fill="url(#bg)"/>`
    : `<rect width="512" height="512" fill="url(#bg)"/>`
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#1b2140"/>
      <stop offset="1" stop-color="#0b0f1e"/>
    </linearGradient>
  </defs>
  ${bg}
  <g transform="translate(${C} ${C}) scale(${scale}) translate(${-C} ${-C})">${artwork()}</g>
</svg>
`
}

const variants = {
  any: svg({ rounded: true, scale: 1 }),
  maskable: svg({ rounded: false, scale: 0.78 }),
  apple: svg({ rounded: false, scale: 0.9 }),
}

writeFileSync(join(out, 'icon.svg'), variants.any)

const pngs = [
  ['icon-192.png', 'any', 192],
  ['icon-512.png', 'any', 512],
  ['icon-maskable-512.png', 'maskable', 512],
  ['apple-touch-icon.png', 'apple', 180],
]

const browser = await chromium.launch()
const page = await browser.newPage()
for (const [file, variant, size] of pngs) {
  await page.setViewportSize({ width: size, height: size })
  await page.setContent(
    `<html><body style="margin:0;background:transparent">${variants[variant].replace('<svg ', `<svg width="${size}" height="${size}" `)}</body></html>`,
  )
  await page.locator('svg').screenshot({ path: join(out, file), omitBackground: true })
}
await browser.close()
console.log(`wrote icon.svg and ${pngs.map((p) => p[0]).join(', ')} to ${out}`)
