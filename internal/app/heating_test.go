package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

// Car heating in the demo: Tuesday's School drop-off leaves home at 07:48,
// so with 15 minutes ahead its time comes at 07:33. The demo outside
// temperature is 2 °C, below the default 3 °C; KNMI's stub has a low of
// 5 °C for Tuesday.

const dropOff = "recurring:1:2026-11-10"

func tue(h, m int) time.Time { return time.Date(2026, 11, 10, h, m, 0, 0, ams) }

func ptr(v float64) *float64 { return &v }

func (ta *testApp) climate() *homeassistant.ClimateStub {
	return ta.app.Service.Clients().Climate.(*homeassistant.ClimateStub)
}

// heating turns car heating on (the demo has its entities chosen) and
// applies change.
func (ta *testApp) heating(change func(map[string]any)) {
	ta.t.Helper()
	var s map[string]any
	ta.json("GET", "/api/settings/heating", nil, http.StatusOK, &s)
	s["enabled"] = true
	if change != nil {
		change(s)
	}
	ta.json("PUT", "/api/settings/heating", s, http.StatusOK, nil)
}

// heatCheck is an EVCC check followed by the car heating job.
func (ta *testApp) heatCheck() {
	ta.t.Helper()
	ta.poll()
	if err := ta.app.Service.CheckHeating(context.Background()); err != nil {
		ta.t.Fatal(err)
	}
}

func (ta *testApp) heatingView() service.HeatingView {
	ta.t.Helper()
	var v service.HeatingView
	ta.json("GET", "/api/heating", nil, http.StatusOK, &v)
	return v
}

// lastRun is the latest heating decision.
func (ta *testApp) lastRun() model.HeatingRun {
	ta.t.Helper()
	v := ta.heatingView()
	if len(v.Recent) == 0 {
		ta.t.Fatal("no heating decision")
	}
	return v.Recent[0]
}

// heatEvents are the fresh events about car heating.
func heatEvents(l *eventLog) []activityEvent {
	var out []activityEvent
	for _, e := range l.fresh() {
		if e.SubjectType == "heating" || e.Action == "notification.action" {
			out = append(out, e)
		}
	}
	return out
}

func TestCarHeatingSettings(t *testing.T) {
	ta := newTestApp(t, "")
	var s model.HeatingSettings
	ta.json("GET", "/api/settings/heating", nil, http.StatusOK, &s)
	want := model.DefaultHeatingSettings()
	want.ClimateEntity, want.OutsideEntity = homeassistant.StubClimateEntity, homeassistant.StubOutsideEntity
	if s != want {
		t.Fatalf("demo settings = %+v", s)
	}
	log := &eventLog{ta: ta}
	log.fresh()
	ta.json("PUT", "/api/settings/heating", map[string]any{"enabled": true, "climateEntity": ""}, http.StatusBadRequest, nil)
	ta.json("PUT", "/api/settings/heating", map[string]any{"leadMin": 90}, http.StatusBadRequest, nil)
	ta.json("PUT", "/api/settings/heating", map[string]any{"targetC": "warm"}, http.StatusBadRequest, nil)
	log.none("invalid settings")
	ta.json("PUT", "/api/settings/heating", map[string]any{"enabled": true, "belowC": 1.5}, http.StatusOK, &s)
	if !s.Enabled || s.BelowC != 1.5 || s.ClimateEntity != homeassistant.StubClimateEntity {
		t.Errorf("saved = %+v", s)
	}
	if e := log.one("settings.update", model.SourceUI); e.Summary != "Changed car heating settings: heat below, car heating" {
		t.Errorf("event = %+v", e)
	}

	// The entities to choose from.
	var sensors []homeassistant.Sensor
	ta.json("GET", "/api/homeassistant/sensors", nil, http.StatusOK, &sensors)
	var kinds []string
	for _, sn := range sensors {
		if sn.Kind == homeassistant.KindClimate || sn.Kind == homeassistant.KindTemperature {
			kinds = append(kinds, sn.EntityID+":"+sn.Kind)
		}
	}
	if strings.Join(kinds, ",") != "climate.demo_car:climate,sensor.demo_outside_temperature:temperature" {
		t.Errorf("climate and temperature sensors = %v", kinds)
	}

	// What it reads now and would send; asking needs notifications.
	v := ta.heatingView()
	if v.Outside.C == nil || *v.Outside.C != 2 || v.Outside.Source != "sensor" || v.Climate.State != "off" ||
		v.Climate.Mode != "heat_cool" || *v.Climate.SendC != 20 || len(v.Recent) != 0 ||
		len(v.Notes) != 1 || !strings.HasPrefix(v.Notes[0], "Asking first needs phone notifications: notifications are off") {
		t.Errorf("view = %+v", v)
	}
	// The demo control sets the outside temperature.
	ta.json("POST", "/api/demo/climate", map[string]any{"outsideC": -3.5}, http.StatusOK, &v)
	if v.Outside.C == nil || *v.Outside.C != -3.5 {
		t.Errorf("after the demo control = %+v", v.Outside)
	}
	ta.json("POST", "/api/demo/climate", map[string]any{"unavailable": true}, http.StatusOK, &v)
	if v.Outside.Source != "forecast" || *v.Outside.C != 4 || !strings.Contains(v.Outside.Note, "unavailable") {
		t.Errorf("unavailable sensor = %+v", v.Outside)
	}
	ta.json("POST", "/api/demo/climate", map[string]any{"outsideC": 99}, http.StatusBadRequest, nil)
}

func TestCarHeatingTurnsOnByItselfWhilePluggedIn(t *testing.T) {
	ta := newTestApp(t, "")
	ta.notifications(func(s map[string]any) { s["questions"], s["early"] = false, false })
	ta.heating(func(s map[string]any) { s["targetC"] = 30 })
	ta.at(tue(7, 0))
	ta.plug(true, 70)
	ta.phone().Take()
	log := &eventLog{ta: ta}
	log.fresh()

	ta.at(tue(7, 32))
	ta.heatCheck()
	if calls := ta.climate().Take(); len(calls) != 0 {
		t.Fatalf("heated too early: %+v", calls)
	}
	ta.at(tue(7, 33))
	ta.heatCheck()
	// 30 °C is more than the demo climate takes: 26 °C.
	if calls := ta.climate().Take(); len(calls) != 1 || calls[0] != (homeassistant.ClimateCall{Entity: homeassistant.StubClimateEntity, Temp: 26, Mode: "heat_cool"}) {
		t.Fatalf("climate calls = %+v", calls)
	}
	ta.expectSent("Car heating on")
	if n := ta.notification("heat_on:" + dropOff); n.Message != "Heating the car to 26 °C: 2 °C outside, School drop-off leaves at 07:48." ||
		n.Urgent || n.State != model.NotifySent {
		t.Errorf("message = %+v", n)
	}
	events := heatEvents(log)
	if len(events) != 1 || events[0].Action != "heating.on" || events[0].Source != model.SourceAutomation ||
		events[0].Summary != "Turned on the car heating for School drop-off: 26 °C, 2 °C outside" ||
		events[0].Related != "trip:"+dropOff || events[0].Detail["plugged"] != "yes" {
		t.Fatalf("events = %+v", events)
	}
	changed(t, events[0], "climate", "off, 18 °C", "heat_cool, 26 °C")
	if r := ta.lastRun(); r.Outcome != model.HeatHeated || r.HeatedAt == nil || r.Plugged == nil || !*r.Plugged ||
		r.OutsideSource != "sensor" || r.NotificationID == 0 {
		t.Errorf("run = %+v", r)
	}
	var status struct {
		Heating *service.HeatingSummary `json:"heating"`
		Climate *service.ClimateStatus  `json:"climate"`
	}
	ta.json("GET", "/api/status", nil, http.StatusOK, &status)
	if status.Heating == nil || status.Heating.Last == nil || status.Heating.Last.Outcome != model.HeatHeated {
		t.Errorf("status = %+v", status.Heating)
	}
	// The Overview's badge: heating, right after it was turned on.
	if c := status.Climate; c == nil || c.Mode != "heat_cool" || c.Action != "heating" || *c.TargetC != 26 {
		t.Errorf("climate = %+v", status.Climate)
	}

	// Once per departure.
	ta.climate().SetState("off")
	ta.at(tue(7, 40))
	ta.heatCheck()
	if calls := ta.climate().Take(); len(calls) != 0 {
		t.Errorf("heated twice: %+v", calls)
	}
	ta.expectSent()
	// Off the phone after half an hour.
	ta.at(tue(8, 2))
	ta.heatCheck()
	ta.expectSent()
	ta.at(tue(8, 3))
	ta.heatCheck()
	ta.expectSent("-Car heating on")
	if n := ta.notification("heat_on:" + dropOff); n.State != model.NotifyDone || n.Note != "Cleared after 30 minutes" {
		t.Errorf("after 30 minutes = %+v", n)
	}
}

// Unplugged it asks first, also in quiet hours; Heat now turns it on.
func TestCarHeatingAsksFirstWhileUnplugged(t *testing.T) {
	ta := newTestApp(t, "")
	ta.notifications(func(s map[string]any) {
		s["questions"], s["early"], s["quietFrom"], s["quietTo"] = false, false, "06:00", "08:00"
	})
	ta.heating(nil)
	ta.phone().Take()
	log := &eventLog{ta: ta}
	log.fresh()

	ta.at(tue(7, 33))
	ta.heatCheck()
	if calls := ta.climate().Take(); len(calls) != 0 {
		t.Fatalf("heated without asking: %+v", calls)
	}
	ta.expectSent("Heat the car?")
	n := ta.notification("heat_ask:" + dropOff)
	if n.Message != "2 °C outside, School drop-off leaves at 07:48. Heat it to 20 °C?" || !n.Urgent ||
		strings.Join(n.Verbs, ",") != "heat,view" {
		t.Fatalf("question = %+v", n)
	}
	if r := ta.lastRun(); r.Outcome != model.HeatAsked || r.Plugged == nil || *r.Plugged || r.NotificationID != n.ID {
		t.Fatalf("run = %+v", r)
	}
	log.fresh()

	press := func() {
		t.Helper()
		ta.json("POST", "/api/demo/notification-action", map[string]any{"action": "EVCCP_HEAT_" + itoa(n.ID)}, http.StatusNoContent, nil)
	}
	ta.at(tue(7, 36))
	press()
	if calls := ta.climate().Take(); len(calls) != 1 || calls[0].Temp != 20 || calls[0].Mode != "heat_cool" {
		t.Fatalf("climate calls = %+v", calls)
	}
	// The question leaves the phone, and a message says the heating is on.
	ta.expectSent("-Heat the car?", "Car heating on")
	if n := ta.notification("heat_on:" + dropOff); n.Message != "Heating the car to 20 °C: 2 °C outside, School drop-off leaves at 07:48." {
		t.Errorf("message = %q", n.Message)
	}
	events := heatEvents(log)
	if len(events) != 2 || events[0].Action != "heating.on" || events[0].Source != model.SourceNotification ||
		events[0].Summary != "Turned on the car heating for School drop-off (Heat now): 20 °C, 2 °C outside" ||
		events[1].Action != "notification.action" || events[1].Summary != `Heat now pressed on "[EVCC Planner] ❄️ Heat the car?": car heating on at 20 °C` {
		t.Fatalf("events = %+v", events)
	}
	if r := ta.lastRun(); r.Outcome != model.HeatConfirmed || r.HeatedAt == nil {
		t.Errorf("run = %+v", r)
	}
	// A second press changes nothing.
	press()
	if calls := ta.climate().Take(); len(calls) != 0 {
		t.Errorf("second press: %+v", calls)
	}
	// The question's half hour passes: nothing else happens; the message
	// leaves the phone half an hour after it came.
	ta.at(tue(8, 5))
	ta.heatCheck()
	ta.expectSent()
	if r := ta.lastRun(); r.Outcome != model.HeatConfirmed {
		t.Errorf("run after the half hour = %+v", r)
	}
	ta.at(tue(8, 6))
	ta.heatCheck()
	ta.expectSent("-Car heating on")
}

// The Car heating on message has its own switch under Settings →
// Notifications.
func TestCarHeatingOnMessageCanBeTurnedOff(t *testing.T) {
	ta := newTestApp(t, "")
	ta.notifications(func(s map[string]any) { s["questions"], s["early"], s["heatingOn"] = false, false, false })
	ta.heating(func(s map[string]any) { s["askUnplugged"] = false })
	ta.phone().Take()
	ta.at(tue(7, 33))
	ta.heatCheck()
	if calls := ta.climate().Take(); len(calls) != 1 {
		t.Fatalf("climate calls = %+v", calls)
	}
	ta.expectSent()
	if r := ta.lastRun(); r.Outcome != model.HeatHeated || r.NotificationID != 0 {
		t.Errorf("run = %+v", r)
	}
}

func TestCarHeatingQuestionWithoutAnAnswer(t *testing.T) {
	ta := newTestApp(t, "")
	ta.notifications(func(s map[string]any) { s["questions"], s["early"] = false, false })
	ta.heating(nil)
	ta.phone().Take()
	ta.at(tue(7, 33))
	ta.heatCheck()
	ta.expectSent("Heat the car?")
	n := ta.notification("heat_ask:" + dropOff)
	log := &eventLog{ta: ta}
	log.fresh()

	ta.at(tue(8, 2))
	ta.heatCheck()
	ta.expectSent()
	ta.at(tue(8, 3))
	ta.heatCheck()
	ta.expectSent("-Heat the car?")
	if r := ta.lastRun(); r.Outcome != model.HeatNoAnswer {
		t.Errorf("run = %+v", r)
	}
	if events := heatEvents(log); len(events) != 1 || events[0].Action != "heating.skip" ||
		events[0].Summary != "Car heating not turned on for School drop-off: no answer within 30 minutes" {
		t.Errorf("events = %+v", events)
	}
	// A late press does nothing.
	ta.json("POST", "/api/demo/notification-action", map[string]any{"action": "EVCCP_HEAT_" + itoa(n.ID)}, http.StatusNoContent, nil)
	if calls := ta.climate().Take(); len(calls) != 0 {
		t.Errorf("late press: %+v", calls)
	}
}

func TestCarHeatingSkips(t *testing.T) {
	for _, c := range []struct {
		name    string
		setup   func(ta *testApp)
		outcome string
		reason  string
	}{
		{"warm", func(ta *testApp) { ta.climate().SetOutside(ptr(8.0), time.Time{}) }, model.HeatWarm, ""},
		{"car away", func(ta *testApp) { ta.phone().MoveCar("not_home", tue(7, 0)) }, model.HeatSkipped,
			"the car isn't home (device_tracker.demo_car is not_home)"},
		{"already on", func(ta *testApp) { ta.climate().SetState("heat_cool") }, model.HeatSkipped,
			"the climate is already on (heat_cool)"},
		{"no notifications to ask", func(ta *testApp) {
			ta.json("PUT", "/api/settings/notifications", map[string]any{"enabled": false}, http.StatusOK, nil)
		}, model.HeatSkipped, "it asks first while unplugged, but notifications are off (Settings → Notifications)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ta := newTestApp(t, "")
			ta.notifications(func(s map[string]any) { s["questions"], s["early"] = false, false })
			ta.heating(nil)
			c.setup(ta)
			ta.phone().Take()
			log := &eventLog{ta: ta}
			log.fresh()
			ta.at(tue(7, 33))
			ta.heatCheck()
			if calls := ta.climate().Take(); len(calls) != 0 {
				t.Errorf("heated: %+v", calls)
			}
			ta.expectSent()
			r := ta.lastRun()
			if r.Outcome != c.outcome || r.Reason != c.reason {
				t.Errorf("run = %+v", r)
			}
			events := heatEvents(log)
			switch {
			case c.outcome == model.HeatWarm && len(events) != 0:
				t.Errorf("a mild day records nothing: %+v", events)
			case c.outcome == model.HeatSkipped && (len(events) != 1 || events[0].Action != "heating.skip" ||
				events[0].Summary != "Car heating not turned on for School drop-off: "+c.reason):
				t.Errorf("events = %+v", events)
			}
			// Decided: a colder reading later changes nothing.
			ta.climate().SetOutside(ptr(-5.0), time.Time{})
			ta.climate().SetState("off")
			ta.phone().MoveCar("home", tue(7, 34))
			ta.at(tue(7, 35))
			ta.heatCheck()
			if calls := ta.climate().Take(); len(calls) != 0 {
				t.Errorf("heated after the decision: %+v", calls)
			}
		})
	}
}

// Without a usable outside reading the forecast's low for the day stands
// in; without either, nothing happens.
func TestCarHeatingFallsBackToTheForecast(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(ta *testApp)
	}{
		{"unavailable", func(ta *testApp) { ta.climate().SetOutside(nil, time.Time{}) }},
		{"old reading", func(ta *testApp) { ta.climate().SetOutside(ptr(1.0), tue(5, 0)) }},
		{"no sensor", func(ta *testApp) { ta.heating(func(s map[string]any) { s["outsideEntity"] = "" }) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			ta := newTestApp(t, "")
			ta.notifications(func(s map[string]any) { s["questions"], s["early"] = false, false })
			// Tuesday's forecast low is 5 °C.
			ta.heating(func(s map[string]any) { s["belowC"], s["askUnplugged"] = 6, false })
			c.setup(ta)
			ta.phone().Take()
			ta.at(tue(7, 33))
			ta.heatCheck()
			if calls := ta.climate().Take(); len(calls) != 1 {
				t.Fatalf("climate calls = %+v", calls)
			}
			ta.expectSent("Car heating on")
			if n := ta.notification("heat_on:" + dropOff); n.Message != "Heating the car to 20 °C: a low of 5 °C forecast, School drop-off leaves at 07:48." {
				t.Errorf("message = %q", n.Message)
			}
			if r := ta.lastRun(); r.OutsideSource != "forecast" || *r.OutsideC != 5 || r.Plugged == nil || *r.Plugged {
				t.Errorf("run = %+v", r)
			}
		})
	}

	// A week later the stub's forecast has no low: skipped, and why.
	ta := newTestApp(t, "")
	ta.heating(func(s map[string]any) { s["outsideEntity"] = "" })
	ta.at(time.Date(2026, 11, 17, 7, 33, 0, 0, ams))
	ta.heatCheck()
	if r := ta.lastRun(); r.Outcome != model.HeatSkipped || r.TripKey != "recurring:1:2026-11-17" ||
		r.Reason != "no outside temperature: no outside temperature sensor, and the weather forecast has no low for Tue 17 Nov" {
		t.Errorf("run = %+v", r)
	}
}

// With EVCC's data too old to say whether the car is plugged in, it counts
// as unplugged: asked first.
func TestCarHeatingAsksWhenEVCCIsTooOld(t *testing.T) {
	ta := newTestApp(t, "")
	ta.notifications(func(s map[string]any) { s["questions"], s["early"] = false, false })
	ta.heating(nil)
	ta.at(tue(7, 0))
	ta.plug(true, 70)
	ta.phone().Take()
	// No EVCC check since 07:00.
	ta.at(tue(7, 33))
	if err := ta.app.Service.CheckHeating(context.Background()); err != nil {
		t.Fatal(err)
	}
	ta.expectSent("Heat the car?")
	if r := ta.lastRun(); r.Outcome != model.HeatAsked || r.Plugged != nil {
		t.Errorf("run = %+v", r)
	}
}

// Plugged in without notifications it heats quietly.
func TestCarHeatingWithoutNotifications(t *testing.T) {
	ta := newTestApp(t, "")
	ta.heating(nil)
	ta.at(tue(7, 0))
	ta.plug(true, 70)
	ta.phone().Take()
	ta.at(tue(7, 33))
	ta.heatCheck()
	if calls := ta.climate().Take(); len(calls) != 1 {
		t.Fatalf("climate calls = %+v", calls)
	}
	ta.expectSent()
	if r := ta.lastRun(); r.Outcome != model.HeatHeated || r.NotificationID != 0 {
		t.Errorf("run = %+v", r)
	}
}

// A failure is tried again at the next checks before the departure.
func TestCarHeatingTriesAgainAfterAFailure(t *testing.T) {
	ta := newTestApp(t, "")
	ta.heating(func(s map[string]any) { s["belowC"], s["askUnplugged"] = 6, false }) // the forecast's 5 °C while HA is down
	log := &eventLog{ta: ta}
	log.fresh()
	ta.climate().SetFail(errors.New("home assistant unreachable"))
	ta.at(tue(7, 33))
	ta.heatCheck()
	r := ta.lastRun()
	if r.Outcome != model.HeatFailed || !r.Retry || r.Tries != 1 || r.Reason != "couldn't read the car's climate: home assistant unreachable" {
		t.Fatalf("run = %+v", r)
	}
	events := heatEvents(log)
	if len(events) != 1 || events[0].Outcome != model.OutcomeFailure ||
		events[0].Summary != "Car heating for School drop-off failed: couldn't read the car's climate: home assistant unreachable; trying again" {
		t.Fatalf("events = %+v", events)
	}
	ta.climate().SetFail(nil)
	ta.at(tue(7, 34))
	ta.heatCheck()
	if calls := ta.climate().Take(); len(calls) != 1 {
		t.Fatalf("climate calls = %+v", calls)
	}
	events = heatEvents(log)
	if len(events) != 1 || events[0].Action != "heating.on" || !strings.HasSuffix(events[0].Summary, " (on try 2)") {
		t.Errorf("events = %+v", events)
	}
	if r := ta.lastRun(); r.Outcome != model.HeatHeated || r.Retry {
		t.Errorf("run = %+v", r)
	}
	if p := ta.activity("?type=heating"); len(p.Events) != 2 { // the settings change is under settings
		t.Errorf("heating activity = %+v", p.Events)
	}
}

// A restart keeps the decision: no second one for the same departure.
func TestCarHeatingDecidesOnceAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	ta := newTestApp(t, dir)
	ta.heating(nil)
	ta.climate().SetOutside(ptr(8.0), time.Time{})
	ta.at(tue(7, 33))
	ta.heatCheck()
	if r := ta.lastRun(); r.Outcome != model.HeatWarm {
		t.Fatalf("run = %+v", r)
	}
	ta.app.Close()
	ta = openTestApp(t, dir, fixedNow)
	ta.at(tue(7, 40)) // the new demo climate reads 2 °C
	ta.heatCheck()
	if calls := ta.climate().Take(); len(calls) != 0 {
		t.Errorf("decided again after the restart: %+v", calls)
	}
}

func TestCarHeatingOffDoesNothing(t *testing.T) {
	ta := newTestApp(t, "")
	ta.at(tue(7, 33))
	ta.heatCheck()
	if calls := ta.climate().Take(); len(calls) != 0 || len(ta.heatingView().Recent) != 0 {
		t.Errorf("car heating is off by default: %+v", calls)
	}
	var status struct {
		Heating *service.HeatingSummary `json:"heating"`
		Climate *service.ClimateStatus  `json:"climate"`
	}
	ta.json("GET", "/api/status", nil, http.StatusOK, &status)
	if status.Heating != nil {
		t.Errorf("status = %+v", status.Heating)
	}
	// The climate badge only needs the climate entity (the demo has one).
	if c := status.Climate; c == nil || c.Mode != "off" || c.Action != "off" {
		t.Errorf("climate = %+v", status.Climate)
	}
	// It follows the car's climate, also when turned on elsewhere, and goes
	// without an entity.
	ta.climate().SetState("cool")
	ta.heatCheck()
	ta.json("GET", "/api/status", nil, http.StatusOK, &status)
	if c := status.Climate; c == nil || c.Action != "cooling" {
		t.Errorf("climate after cooling = %+v", status.Climate)
	}
	ta.json("PUT", "/api/settings/heating", map[string]any{"climateEntity": ""}, http.StatusOK, nil)
	status.Climate = nil
	ta.json("GET", "/api/status", nil, http.StatusOK, &status)
	if status.Climate != nil {
		t.Errorf("climate without an entity = %+v", status.Climate)
	}
}
