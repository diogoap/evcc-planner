package model

import "testing"

func TestAdviceSettingsChargeExtraSteps(t *testing.T) {
	d := DefaultAdviceSettings()
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if l := d.Level(IndicationMedium); !l.Enabled || l.Steps[0] != (ExtraStep{30, 40}) {
		t.Errorf("medium = %+v", l)
	}
	if l := d.Level(IndicationHigh); l.Enabled || l.Steps[2] != (ExtraStep{75, 70}) {
		t.Errorf("high = %+v", l)
	}
	if l := d.Level("unknown"); l.Steps[0] != (ExtraStep{20, 50}) {
		t.Errorf("unknown indications use little: %+v", l)
	}
	for _, c := range []struct {
		name string
		edit func(*AdviceSettings)
		want string
	}{
		{"two steps", func(a *AdviceSettings) { a.ExtraLittle.Steps = a.ExtraLittle.Steps[:2] }, "charge extra (little) needs three steps"},
		{"discount not rising", func(a *AdviceSettings) { a.ExtraMedium.Steps[1].MinDiscountPct = 30 },
			"charge extra (medium): each step needs a bigger discount than the one before"},
		{"discount over 100", func(a *AdviceSettings) { a.ExtraHigh.Steps[2].MinDiscountPct = 120 },
			"charge extra (high): discounts must be between 0 and 100%"},
		{"target falling", func(a *AdviceSettings) { a.ExtraLittle.Steps[2].TargetSoC = 70 },
			"charge extra (little): targets must not go down from step to step"},
		{"target too low", func(a *AdviceSettings) { a.ExtraLittle.Steps[0].TargetSoC = 5 },
			"charge extra (little): targets must be between 10 and 100%"},
	} {
		a := DefaultAdviceSettings()
		c.edit(&a)
		if err := a.Validate(); err == nil || err.Error() != c.want {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	// Automatic plans need nothing more: they follow charge extra's blocks.
	a := DefaultAdviceSettings()
	a.AutoApply = true
	if err := a.Validate(); err != nil {
		t.Errorf("auto: %v", err)
	}
}
