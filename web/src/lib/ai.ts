import type { AIDayResult, AIMacro, AIModelOption, AITrigger } from '../api/types'
import { clock, dayMonth, localDate } from './format'

/** What started an AI run. */
export const triggerNames: Record<AITrigger, string> = {
  prices: 'New prices',
  plug_in: 'Car plugged in',
  manual: 'Run by hand',
}

/** "{{trips}}" */
export const macroText = (name: string) => `{{${name}}}`

const placeholder = /\{\{\s*([a-zA-Z0-9_]+)\s*\}\}/g

/** Placeholders in a prompt that aren't macros, each once, as the server checks them. */
export function unknownMacros(prompt: string, macros: AIMacro[]) {
  const known = new Set(macros.map((m) => m.name))
  const out: string[] = []
  for (const m of prompt.matchAll(placeholder)) {
    const name = m[1]!
    if (!known.has(name.toLowerCase()) && !out.includes(name)) out.push(name)
  }
  return out
}

/** Macros the prompt leaves out. */
export function unusedMacros(prompt: string, macros: AIMacro[]) {
  const used = new Set([...prompt.matchAll(placeholder)].map((m) => m[1]!.toLowerCase()))
  return macros.filter((m) => !used.has(m.name))
}

/** The text with a placeholder put in at the cursor (replacing a selection), and the cursor after it. */
export function insertMacro(text: string, start: number, end: number, name: string) {
  const insert = macroText(name)
  return { text: text.slice(0, start) + insert + text.slice(end), cursor: start + insert.length }
}

// "01:00–05:00", or "Mon 9 Nov 23:00–05:00" when the window starts on another day than it ends.
function window(from?: string, until?: string) {
  if (!from || !until) return ''
  const start =
    localDate(from) === localDate(until) ? clock(from) : `${dayMonth(localDate(from))} ${clock(from)}`
  return `${start}–${clock(until)}`
}

/** A day's AI recommendation in a few words: "Charge extra to 75%, 01:00–05:00". */
export function recommendationText(d: AIDayResult) {
  switch (d.recommendation) {
    case 'charge_extra':
      return `Charge extra to ${d.targetSoc}%, ${window(d.from, d.until)}`
    case 'solar':
      return `Charge extra from solar, ${window(d.from, d.until)}`
    case 'defer': {
      if (!d.deferUntil) return 'Defer'
      const pause = d.postpone ? ', pausing cheap-grid charging' : ''
      return `Defer until ${dayMonth(d.deferUntil)}${pause}`
    }
    default:
      return 'Nothing extra'
  }
}

/** A US dollar amount in cents, below $0.10 with two significant digits: "$0.20", "$0.0029", "$40". */
export function usd(v: number) {
  if (Number.isInteger(v)) return `$${v}`
  return `$${v >= 0.1 ? v.toFixed(2) : Number(v.toPrecision(2))}`
}

/** A price per million tokens, in cents where it has them: "$0.10", "$2.50", "$0.036", "$40". */
export function perMillion(v: number) {
  if (Number.isInteger(v)) return `$${v}`
  return `$${Number(v.toFixed(2)) === v ? v.toFixed(2) : v}`
}

/** A model in the list: "~anthropic/claude-haiku-latest · ≈ $0.0030/run (default)". */
export function modelLabel(m: AIModelOption, defaultModel: string) {
  return `${m.id} · ≈ ${usd(m.runUsd)}/run${m.id === defaultModel ? ' (default)' : ''}`
}

/** What a model costs a month at the automatic runs a day: "≈ $0.27 a month at 3 runs a day". */
export function monthlyText(m: AIModelOption, runsPerDay: number) {
  if (!(runsPerDay > 0)) return 'No automatic runs: only runs by hand cost'
  return `≈ ${usd(m.runUsd * runsPerDay * 30)} a month at ${runsPerDay} run${runsPerDay === 1 ? '' : 's'} a day`
}

/** "about 2,300 tokens" */
export function tokensText(n: number) {
  return `about ${(Math.round(n / 100) * 100).toLocaleString('en-GB')} tokens`
}
