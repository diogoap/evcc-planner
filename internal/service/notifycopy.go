package service

import "github.com/diogoap/evcc-planner/internal/model"

// NotificationCopy is what a kind of notification says, shown under
// Settings → Notifications. In Title and Messages, {…} is filled in when it
// is sent and […] only appears when it applies. On the phone the title
// starts with TitlePrefix and, in AI mode, ends with AISuffix. API tests
// check every notification they send against it.
type NotificationCopy struct {
	Setting     string   `json:"setting"` // the setting it belongs to: a switch's key, "test", "urgent", or "evening"
	Kind        string   `json:"kind"`
	TitlePrefix string   `json:"titlePrefix"` // "[EVCC Planner] 🔌"
	Title       string   `json:"title"`
	AISuffix    string   `json:"aiSuffix"` // "(🤖 AI mode)", or empty when it doesn't say the mode
	Messages    []string `json:"messages"` // one of these
	Note        string   `json:"note,omitempty"`
	Buttons     []string `json:"buttons"`
	ai          bool
}

const extraPlanWhat = `{about the plan}: “The extra plan was created automatically.”, ` +
	`“The plan is created automatically when the day starts.”, or “Create the plan in the planner.”`

// planAtOnce is how plan messages made at the same time are sent.
const planAtOnce = "When a plan is sent for a trip and an extra plan is created at the same time, only the message with the higher target comes."

// extraAtOnce is how a trip's plug-in reminder and charge extra's, due at
// the same time, are sent.
const extraAtOnce = "When charge extra's plug-in reminder is due at the same time, it comes in this message."

// NotificationCopies lists every notification the planner sends.
func NotificationCopies() []NotificationCopy {
	charge := []string{model.VerbSnooze, model.VerbSkip, model.VerbView}
	view := []string{model.VerbView}
	list := []NotificationCopy{
		{Setting: "test", Kind: model.NotifyTest, Title: "Test notification",
			Messages: []string{"This phone gets plug-in reminders."}, Buttons: view,
			Note: "Sent by Send test notification."},
		{Setting: "early", Kind: model.NotifyEarly, Title: "Plug in to charge cheaply", Buttons: charge, ai: true,
			Messages: []string{
				"Charging is cheapest from {time, or now} ({price}/kWh); {trip} needs {target}% by {time}.[ {advice}[ {about the plan}]]",
				"Charging is cheapest from {time, or now} ({price}/kWh); the plan charges {target}% by {time}, also for {later trip}.[ {advice}[ {about the plan}]]",
			},
			Note: extraAtOnce + " " + extraPlanWhat},
		{Setting: "urgent", Kind: model.NotifyUrgent, Title: "Plug in by {latest plug-in time}", Buttons: charge, ai: true,
			Messages: []string{"{trip} needs {target}% by {departure}.[ {advice}[ {about the plan}]]"},
			Note:     extraAtOnce + " " + extraPlanWhat},
		{Setting: "urgent", Kind: model.NotifyLate, Title: "Plug in now", ai: true,
			Buttons:  []string{model.VerbSkip, model.VerbView},
			Messages: []string{"Plugging in now gives about {SoC}% by {departure} ({trip} needs {target}%)."},
			Note:     "Once the latest plug-in time has passed."},
		{Setting: "urgent", Kind: model.NotifyInfo, Title: "Trip skipped", Buttons: view, ai: true,
			Messages: []string{"The next target is {target}% by {time} for {trip}.", "No trip needs charging now."},
			Note:     "After Skip trip on a reminder."},
		{Setting: "problems", Kind: model.NotifyStopped, Title: "Charging stopped", Buttons: view, ai: true,
			Messages: []string{"Charging stopped at {SoC}%; {trip} needs {target}% by {departure}."}},
		{Setting: "problems", Kind: model.NotifyUnplugged, Title: "Unplugged early", Buttons: view, ai: true,
			Messages: []string{"Unplugged at {SoC}%; {trip} needs {target}% by {departure}."}},
		{Setting: "problems", Kind: model.NotifyMode, Title: "Charging is off in EVCC", Buttons: view, ai: true,
			Messages: []string{"The plan won't run; {trip} needs {target}% by {departure}. Set EVCC to Smart."}},
		{Setting: "problems", Kind: model.NotifyMode, Title: "EVCC is in Fast mode", Buttons: view, ai: true,
			Messages: []string{"It charges at full power now, not in the plan's cheapest slots or from solar. Set EVCC to Smart."}},
		{Setting: "problems", Kind: model.NotifyMode, Title: "EVCC is in {mode} mode", Buttons: view, ai: true,
			Messages: []string{"It doesn't follow the plan; {trip} needs {target}% by {departure}. Set EVCC to Smart."}},
		{Setting: "problems", Kind: model.NotifyMissed, Title: "Target missed", Buttons: view, ai: true,
			Messages: []string{"Left for {trip} with {SoC}% (target {target}%)."}},
		{Setting: "questions", Kind: model.NotifyQuestion, Title: "Is this a car trip?",
			Buttons:  []string{model.VerbCount, model.VerbNoTrip, model.VerbView},
			Messages: []string{`"{event}" on {day} at {time}, {destination}.`}},
		{Setting: "ready", Kind: model.NotifyReady, Title: "Ready for {trip}", Buttons: view, ai: true,
			Messages: []string{"Charged to {SoC}% for {trip} at {time}."}},
		{Setting: "solar", Kind: model.NotifySolar, Title: "Plug in for the sun", ai: true,
			Buttons: []string{model.VerbSnooze, model.VerbView},
			Messages: []string{"Sunshine until about {time} charges the car at about €{price}/kWh " +
				"(€{price} without the tax on own solar), {compared with}. Plug in to use it."},
			Note: "{compared with}: “no more than tomorrow's lowest price”, “no more than {day}'s expected lowest price”, " +
				"“no more than this week's lowest prices”, or in AI mode “which the AI plan advises to use”."},
		{Setting: "extra", Kind: model.NotifyExtra, Title: "Plug in for charge extra", ai: true,
			Buttons: []string{model.VerbSnooze, model.VerbView},
			Messages: []string{
				"{advice} Plug in so the plan can charge.",
				"{advice} The plan is created automatically when the day starts; plug in so it can charge.",
				"{advice} Create the plan in the planner and plug in.",
			},
			Note: "{advice}: the day's charge extra advice, such as “Charge extra to 60%: about 4 h between 00:15 and 05:45 …”. " +
				"When a plug-in reminder for the next trip is due at the same time, it comes in that message instead."},
		{Setting: "extraPlan", Kind: model.NotifyExtraPlan, Title: "Extra plan created", Buttons: view, ai: true,
			Messages: []string{"{advice} The plan asks EVCC for {target}% by {time} and ends then."},
			Note:     planAtOnce},
		{Setting: "prices", Kind: model.NotifyPrices, Title: "New prices: plug in", Buttons: view, ai: true,
			Messages: []string{
				"The plan for {trip} is now {target}% by {time}[ (was {target}%)][, also for {later trip}]. Plug in so it can charge.",
				"{advice}[ {about the plan}] Plug in so it can charge.",
			},
			Note: "When both apply, they come in one message. " + extraPlanWhat},
		{Setting: "evening", Kind: model.NotifyEvening, Title: "Plug in tonight", ai: true,
			Buttons: []string{model.VerbSnooze, model.VerbView},
			Messages: []string{
				"Charging is cheapest from {time, or now} ({price}/kWh); {trip} needs {target}% by {time}.[ {advice}[ {about the plan}]]",
				"Charging is cheapest from {time, or now} ({price}/kWh); the plan charges {target}% by {time}, also for {later trip}.[ {advice}[ {about the plan}]]",
				"{advice}[ {about the plan}]",
			},
			Note: "At the evening reminder time, when charging is planned for tonight. When both apply, they come in one message. " +
				extraPlanWhat},
		{Setting: "planSent", Kind: model.NotifyPlanSent, Title: "Plan sent", Buttons: view, ai: true,
			Messages: []string{"EVCC charges to {target}% by {time} for {trip}[, with {later trip} charged ahead]."},
			Note:     "Once per departure: later changes to its plan come without a message. " + planAtOnce},
		{Setting: "heating", Kind: model.NotifyHeatAsk, Title: "Heat the car?", Buttons: []string{model.VerbHeat, model.VerbView},
			Messages: []string{"{outside}, {trip} leaves at {time}. Heat it to {target} °C?"},
			Note: "{outside}: “1 °C outside” from the outside temperature sensor, or “A low of 1 °C forecast” from the " +
				"weather forecast. Also in quiet hours; cleared after 30 minutes without an answer."},
		{Setting: "heatingOn", Kind: model.NotifyHeatOn, Title: "Car heating on", Buttons: view,
			Messages: []string{"Heating the car to {target} °C: {outside}, {trip} leaves at {time}."},
			Note:     "When car heating turns the climate on, by itself or after Heat now. Also in quiet hours; cleared after 30 minutes."},
		{Setting: "arrival", Kind: model.NotifyArrived, Title: "Back home: plug in", ai: true,
			Buttons: []string{model.VerbSnooze, model.VerbView},
			Messages: []string{
				"{trip} needs {target}% by {departure}.[ {advice}[ Create the plan in the planner.]]",
				"The plan charges {target}% by {time}, also for {later trip}.[ {advice}[ Create the plan in the planner.]]",
				"{advice}[ Create the plan in the planner.]",
			}},
	}
	for i := range list {
		c := &list[i]
		c.TitlePrefix = titlePrefix(c.Kind)
		if c.ai {
			c.AISuffix = AILabel
		}
	}
	return list
}
