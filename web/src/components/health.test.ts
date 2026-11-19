import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import HealthLine from './HealthLine.vue'

const now = '2026-11-09T18:00:00Z' // Monday 19:00 in Amsterdam

const line = (issues: InstanceType<typeof HealthLine>['$props']['issues']) =>
  mount(HealthLine, { props: { issues, now }, global: { stubs: { RouterLink: RouterLinkStub } } })

describe('HealthLine', () => {
  it('shows nothing while every connection works', () => {
    expect(line([]).find('[data-testid=health-line]').exists()).toBe(false)
  })

  it('says what failed, when it last worked, and links to the connections', () => {
    const w = line([
      { name: 'Home Assistant', detail: 'connection refused', lastSuccessAt: '2026-11-09T17:42:00Z' },
      { name: 'Google Calendar', detail: 'Demo calendar: HTTP 404', lastSuccessAt: '2026-11-08T17:00:00Z' },
      { name: 'KNMI', detail: 'HTTP 401 (check the KNMI API key)' },
    ])
    expect(w.get('[data-testid=health-line]').classes()).toContain('tone-warn')
    expect(w.get('[data-testid="health-Home Assistant"]').text()).toBe(
      'Home Assistant: connection refused · last worked 18:42',
    )
    expect(w.get('[data-testid="health-Google Calendar"]').text()).toBe(
      'Google Calendar: Demo calendar: HTTP 404 · last worked Sun 8 Nov, 18:00',
    )
    expect(w.get('[data-testid="health-KNMI"]').text()).toBe('KNMI: HTTP 401 (check the KNMI API key)')
    expect(w.getComponent(RouterLinkStub).props('to')).toEqual({
      name: 'settings',
      query: { tab: 'connections' },
    })
  })
})
