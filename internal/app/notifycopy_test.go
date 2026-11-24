package app

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

// copyPattern turns a notification copy template into a regular
// expression: {…} is any text, […] is optional.
func copyPattern(tpl string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`(?s)^`)
	for i := 0; i < len(tpl); i++ {
		switch tpl[i] {
		case '[':
			b.WriteString(`(?:`)
		case ']':
			b.WriteString(`)?`)
		case '{':
			b.WriteString(`.+?`)
			i += strings.IndexByte(tpl[i:], '}')
		default:
			b.WriteString(regexp.QuoteMeta(tpl[i : i+1]))
		}
	}
	b.WriteString(`$`)
	return regexp.MustCompile(b.String())
}

// copyMatches reports whether a notification says what Settings →
// Notifications shows for its kind.
func copyMatches(n model.Notification) bool {
	for _, c := range service.NotificationCopies() {
		// Recorded without sending (a missed target with notifications
		// off), a title has no tag.
		title := strings.TrimPrefix(n.Title, c.TitlePrefix+" ")
		if c.AISuffix != "" {
			title = strings.TrimSuffix(title, " "+c.AISuffix)
		}
		if c.Kind != n.Kind || !copyPattern(c.Title).MatchString(title) {
			continue
		}
		for _, m := range c.Messages {
			if copyPattern(m).MatchString(n.Message) {
				return true
			}
		}
	}
	return false
}

// checkCopy fails the test when a notification it sent is not in the
// copy shown under Settings → Notifications. A closed store is skipped.
func checkCopy(t *testing.T, a *App) {
	list, err := a.Store.RecentNotifications(10000)
	if err != nil {
		return
	}
	for _, n := range list {
		if !copyMatches(n) {
			t.Errorf("notification %s (%q: %q) does not match its copy in service.NotificationCopies", n.Kind, n.Title, n.Message)
		}
	}
}

func TestNotificationCopyCoversEveryKind(t *testing.T) {
	ta := newTestApp(t, "")
	var list []service.NotificationCopy
	ta.json("GET", "/api/notifications/copy", nil, http.StatusOK, &list)
	kinds := map[string]bool{}
	settings := []string{"test", "early", "urgent", "problems", "questions", "ready", "solar", "extra", "prices", "arrival", "evening", "planSent"}
	verbs := []string{model.VerbSnooze, model.VerbSkip, model.VerbView, model.VerbCount, model.VerbNoTrip}
	for _, c := range list {
		kinds[c.Kind] = true
		// Every title starts with the app's tag and an emoji; all but the
		// test and questions say when AI mode is on.
		emoji, tagged := strings.CutPrefix(c.TitlePrefix, service.AppTag+" ")
		label := service.AILabel
		if c.Kind == model.NotifyTest || c.Kind == model.NotifyQuestion {
			label = ""
		}
		if !slices.Contains(settings, c.Setting) || len(c.Messages) == 0 || len(c.Buttons) == 0 ||
			!tagged || emoji == "" || c.AISuffix != label {
			t.Errorf("copy = %+v", c)
		}
		for _, v := range c.Buttons {
			if !slices.Contains(verbs, v) {
				t.Errorf("%s: unknown button %q", c.Kind, v)
			}
		}
	}
	for _, k := range []string{model.NotifyTest, model.NotifyEarly, model.NotifyUrgent, model.NotifyLate, model.NotifyStopped,
		model.NotifyUnplugged, model.NotifyMode, model.NotifyMissed, model.NotifyQuestion, model.NotifyReady, model.NotifySolar,
		model.NotifyExtra, model.NotifyExtraPlan, model.NotifyArrived, model.NotifyPrices, model.NotifyInfo, model.NotifyEvening} {
		if !kinds[k] {
			t.Errorf("no copy for %s", k)
		}
	}

	// The patterns: {…} any text, […] optional.
	for _, c := range []struct {
		tpl, text string
		want      bool
	}{
		{"Plug in by {time}", "Plug in by 06:36", true},
		{"Plug in by {time}", "Plug in now", false},
		{"Now {target}%[ (was {target}%)].", "Now 64%.", true},
		{"Now {target}%[ (was {target}%)].", "Now 64% (was 42%).", true},
		{"Now {target}%[ (was {target}%)].", "Now 64% (is 42%)", false},
	} {
		if got := copyPattern(c.tpl).MatchString(c.text); got != c.want {
			t.Errorf("%q matches %q: %v, want %v", c.tpl, c.text, got, c.want)
		}
	}
}
