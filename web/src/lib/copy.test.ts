import { describe, expect, it } from 'vitest'
import { copySegments } from './copy'

describe('copySegments', () => {
  it('marks placeholders and optional brackets', () => {
    expect(copySegments('Now {target}%[ (was {target}%)].')).toEqual([
      { text: 'Now ', kind: 'text' },
      { text: '{target}', kind: 'slot' },
      { text: '%', kind: 'text' },
      { text: '[', kind: 'bracket' },
      { text: ' (was ', kind: 'text' },
      { text: '{target}', kind: 'slot' },
      { text: '%)', kind: 'text' },
      { text: ']', kind: 'bracket' },
      { text: '.', kind: 'text' },
    ])
  })

  it('keeps plain text whole', () => {
    expect(copySegments('Plug in now')).toEqual([{ text: 'Plug in now', kind: 'text' }])
  })
})
