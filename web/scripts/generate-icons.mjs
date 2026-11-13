// Generates the app icons (SVG favicon, PWA and Apple touch PNGs) and the iOS
// launch images from one design: EVCC's green charging bolt inside a
// clock-like ring whose arc ends in an arrow (planning and automation). Run
// with `pnpm icons`.
import { chromium } from '@playwright/test'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import * as prettier from 'prettier'

const out = join(dirname(fileURLToPath(import.meta.url)), '..', 'public')
const indexFile = join(out, '..', 'index.html')

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

// iOS launch images, shown while the installed app starts (Android draws its
// own from the manifest). Portrait, for the iPhone screens current iOS
// supports, in CSS px with their pixel ratio, in light and dark. The colours
// match --bg and --text in src/styles.css.
const iphones = [
  [375, 667, 2], // SE
  [375, 812, 3], // 11 Pro, 12 and 13 mini
  [390, 844, 3], // 12 to 14
  [393, 852, 3], // 14 Pro to 16
  [402, 874, 3], // 16 Pro, 17
  [414, 896, 2], // 11
  [414, 896, 3], // 11 Pro Max
  [420, 912, 3], // Air
  [428, 926, 3], // 12 and 13 Pro Max, 14 Plus
  [430, 932, 3], // 14 Pro Max to 16 Plus
  [440, 956, 3], // 16 Pro Max, 17 Pro Max
]
const themes = {
  light: { bg: '#f3f3f7', text: '#28293e' },
  dark: { bg: '#0b0f1e', text: '#e8e9f1' },
}
mkdirSync(join(out, 'splash'), { recursive: true })
const links = []
for (const ratio of [2, 3]) {
  const splash = await browser.newPage({ deviceScaleFactor: ratio })
  for (const [width, height] of iphones.filter((d) => d[2] === ratio)) {
    await splash.setViewportSize({ width, height })
    for (const [theme, c] of Object.entries(themes)) {
      const file = `splash/iphone-${width}x${height}@${ratio}x-${theme}.png`
      await splash.setContent(
        `<html><body style="margin:0;height:100vh;display:grid;place-content:center;justify-items:center;gap:20px;` +
          `background:${c.bg};color:${c.text};font:600 22px system-ui,-apple-system,sans-serif">` +
          `${variants.any.replace('<svg ', '<svg width="128" height="128" ')}<div>Trip Planner</div></body></html>`,
      )
      await splash.screenshot({ path: join(out, file) })
      links.push(
        `<link rel="apple-touch-startup-image" media="(device-width: ${width}px) and (device-height: ${height}px)` +
          ` and (-webkit-device-pixel-ratio: ${ratio}) and (orientation: portrait) and (prefers-color-scheme: ${theme})"` +
          ` href="/${file}" />`,
      )
    }
  }
  await splash.close()
}
await browser.close()

// The links go between the splash markers in index.html.
const html = readFileSync(indexFile, 'utf8')
const markers = /<!-- splash:start -->[\s\S]*?<!-- splash:end -->/
if (!markers.test(html)) throw new Error('index.html has no splash markers')
const updated = html.replace(markers, `<!-- splash:start -->\n${links.join('\n')}\n<!-- splash:end -->`)
const options = { ...(await prettier.resolveConfig(indexFile)), filepath: indexFile }
writeFileSync(indexFile, await prettier.format(updated, options))
console.log(
  `wrote icon.svg, ${pngs.map((p) => p[0]).join(', ')}, and ${links.length} launch images to ${out}`,
)
