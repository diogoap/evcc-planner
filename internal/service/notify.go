package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
)

// ---- settings ----

// NotificationSettings returns the phone notification settings.
func (s *Service) NotificationSettings() (model.NotificationSettings, error) {
	return s.Store.NotificationSettings(model.DefaultNotificationSettings())
}

var notificationLabels = map[string]string{
	"enabled": "notifications", "phones": "phones", "linkUrl": "link address", "locationEntity": "car location sensor",
	"early": "early reminders", "earlyLeadMin": "early reminder time", "urgentLeadMin": "urgent reminder time",
	"quietFrom": "quiet hours", "quietTo": "quiet hours", "urgentInQuiet": "urgent reminders in quiet hours",
	"snoozeMin": "snooze", "problems": "charging problems", "questions": "questions", "ready": "ready messages",
}

// SaveNotificationSettings validates and stores the notification settings.
func (s *Service) SaveNotificationSettings(ctx context.Context, v model.NotificationSettings) (model.NotificationSettings, error) {
	v.LinkURL = strings.TrimSpace(v.LinkURL)
	v.LocationEntity = strings.TrimSpace(v.LocationEntity)
	if v.Phones == nil {
		v.Phones = []string{}
	}
	if err := v.Validate(); err != nil {
		return v, ValidationError{Msg: err.Error()}
	}
	ev := model.AuditEvent{Action: "settings.update", SubjectType: "settings", SubjectID: "notifications"}
	err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		before, err := tx.NotificationSettings(model.DefaultNotificationSettings())
		if err != nil {
			return err
		}
		if err := tx.SaveNotificationSettings(v); err != nil {
			return err
		}
		e.Before, e.After = changes(before, v)
		e.Summary = "Saved notification settings (no changes)"
		if fields := fieldList(e.After, notificationLabels); fields != "" {
			e.Summary = "Changed notification settings: " + fields
		}
		return nil
	})
	// The Overview shows the car's location from the new sensor right away.
	if cs := s.state(); err == nil && cs.HA != nil {
		s.pollLocation(ctx, cs)
	}
	return v, err
}

// Phones lists the phones Home Assistant can notify.
func (s *Service) Phones(ctx context.Context) ([]string, error) {
	n := s.Clients().Notify
	if n == nil {
		return nil, invalid("%s", s.Clients().NotifyNote)
	}
	return n.Phones(ctx)
}

// ---- sending ----

// actionPrefix starts the ID of every button the planner puts on a
// notification, so presses for other apps are ignored.
const actionPrefix = "EVCCP_"

func actionID(verb string, id int64) string {
	return actionPrefix + strings.ToUpper(verb) + "_" + strconv.FormatInt(id, 10)
}

// parseAction splits a button ID into its verb and notification ID.
func parseAction(action string) (string, int64, bool) {
	rest, ok := strings.CutPrefix(action, actionPrefix)
	if !ok {
		return "", 0, false
	}
	verb, idText, ok := strings.Cut(rest, "_")
	id, err := strconv.ParseInt(idText, 10, 64)
	if !ok || err != nil {
		return "", 0, false
	}
	return strings.ToLower(verb), id, true
}

func notifyTag(n model.Notification) string { return "evcc-planner-" + strconv.FormatInt(n.ID, 10) }

func verbTitle(verb string, settings model.NotificationSettings) string {
	switch verb {
	case model.VerbSnooze:
		return fmt.Sprintf("Remind in %d min", settings.SnoozeMin)
	case model.VerbSkip:
		return "Skip trip"
	case model.VerbView:
		return "View plan"
	case model.VerbCount:
		return "Count it"
	case model.VerbNoTrip:
		return "Not a trip"
	}
	return verb
}

// deliver sends a notification to the chosen phones and stores it with
// the outcome, recorded in the activity log. A new notification (ID 0) is
// stored first, so its buttons can name it.
func (s *Service) deliver(ctx context.Context, n *model.Notification, settings model.NotificationSettings) error {
	notifier := s.Clients().Notify
	if notifier == nil {
		return invalid("%s", s.Clients().NotifyNote)
	}
	now := s.now()
	if n.ID == 0 {
		n.CreatedAt, n.State = now, model.NotifySent
		saved, err := s.Store.SaveNotification(*n)
		if err != nil {
			return err
		}
		*n = saved
	}
	msg := homeassistant.Message{Title: n.Title, Text: n.Message, Tag: notifyTag(*n), Urgent: n.Urgent, Link: settings.LinkURL}
	for _, v := range n.Verbs {
		switch {
		case v != model.VerbView:
			msg.Actions = append(msg.Actions, homeassistant.Action{ID: actionID(v, n.ID), Title: verbTitle(v, settings)})
		case settings.LinkURL != "":
			msg.Actions = append(msg.Actions, homeassistant.Action{Title: verbTitle(v, settings), URI: settings.LinkURL})
		}
	}
	var errs []error
	for _, p := range settings.Phones {
		if err := notifier.Notify(ctx, p, msg); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
	}
	ev := model.AuditEvent{
		Action: "notification.send", SubjectType: "notification", SubjectID: strconv.FormatInt(n.ID, 10),
		Summary: fmt.Sprintf("Sent %q to %s", n.Title, plural(len(settings.Phones)-len(errs), "phone")),
		Detail:  audit.Marshal(map[string]any{"kind": n.Kind, "message": n.Message}),
	}
	if n.Subject != "" {
		ev.Related = "trip:" + n.Subject
	}
	err := errors.Join(errs...)
	if err != nil && len(errs) == len(settings.Phones) {
		n.State, n.Note = model.NotifyFailed, err.Error()
		ev.Summary = fmt.Sprintf("Sending %q failed", n.Title)
		ev.Outcome, ev.Error = model.OutcomeFailure, err.Error()
		s.record(ctx, ev)
		_, serr := s.Store.SaveNotification(*n)
		return errors.Join(err, serr)
	}
	n.State, n.SentAt, n.SnoozedUntil = model.NotifySent, &now, nil
	if err != nil {
		ev.Error = err.Error()
	}
	s.Log.Info("notification sent", "kind", n.Kind, "title", n.Title)
	return s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		_, err := tx.SaveNotification(*n)
		return err
	})
}

// resolve marks a notification done and clears it from the phones.
func (s *Service) resolve(ctx context.Context, n *model.Notification, note string, settings model.NotificationSettings) error {
	now := s.now()
	wasOnPhone := n.State == model.NotifySent
	n.State, n.Note, n.ResolvedAt, n.SnoozedUntil = model.NotifyDone, note, &now, nil
	if _, err := s.Store.SaveNotification(*n); err != nil {
		return err
	}
	if notifier := s.Clients().Notify; notifier != nil && wasOnPhone {
		for _, p := range settings.Phones {
			if err := notifier.Clear(ctx, p, notifyTag(*n)); err != nil {
				s.Log.Warn("clearing a notification failed", "phone", p, "err", err)
			}
		}
	}
	return nil
}

// SendTestNotification sends a notification right away to check the setup.
func (s *Service) SendTestNotification(ctx context.Context) (*model.Notification, error) {
	settings, err := s.NotificationSettings()
	if err != nil {
		return nil, err
	}
	if len(settings.Phones) == 0 {
		return nil, invalid("choose a phone first")
	}
	n := &model.Notification{
		// Unique even with a fixed clock (tests, demo).
		Key: "test:" + strconv.FormatInt(time.Now().UnixNano(), 36), Kind: model.NotifyTest, Title: "EVCC Trip Planner",
		Message: "Test notification: this phone gets plug-in reminders.", Verbs: []string{model.VerbView},
	}
	if err := s.deliver(ctx, n, settings); err != nil {
		return n, err
	}
	n.State, n.Note = model.NotifyDone, "Test"
	_, err = s.Store.SaveNotification(*n)
	return n, err
}

// RecentNotifications lists the newest notifications.
func (s *Service) RecentNotifications() ([]model.Notification, error) {
	return s.Store.RecentNotifications(20)
}

// ---- buttons ----

// HandleAction acts on a button pressed on a phone notification. Buttons
// of other apps are ignored; a button of a notification that is no longer
// open changes nothing.
func (s *Service) HandleAction(ctx context.Context, action string) error {
	verb, id, ok := parseAction(action)
	if !ok {
		return nil
	}
	ctx = audit.WithSource(ctx, model.SourceNotification)
	n, err := s.Store.Notification(id)
	if IsNotFound(err) {
		return invalid("unknown notification %d", id)
	}
	if err != nil {
		return err
	}
	settings, err := s.NotificationSettings()
	if err != nil {
		return err
	}
	if !n.Open() {
		s.Log.Info("button of a closed notification", "id", id, "verb", verb)
		return nil
	}
	title := verbTitle(verb, settings)
	ev := model.AuditEvent{Action: "notification.action", SubjectType: "notification", SubjectID: strconv.FormatInt(n.ID, 10)}
	if n.Subject != "" {
		ev.Related = "trip:" + n.Subject
	}
	off, on := false, true
	switch verb {
	case model.VerbSnooze:
		until := s.now().Add(time.Duration(settings.SnoozeMin) * time.Minute)
		if n.Latest != nil && until.After(*n.Latest) {
			until = *n.Latest
		}
		n.State, n.SnoozedUntil = model.NotifySnoozed, &until
		n.Note = "Snoozed until " + until.In(s.loc()).Format("15:04")
		ev.Summary = fmt.Sprintf("%s pressed: %q snoozed until %s", title, n.Title, until.In(s.loc()).Format("15:04"))
		return s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
			_, err := tx.SaveNotification(n)
			return err
		})
	case model.VerbSkip, model.VerbNoTrip, model.VerbCount:
		in := TripInput{Enabled: &off}
		note := "Trip turned off"
		if verb == model.VerbCount {
			in, note = TripInput{Reviewed: &on}, "Counted as a car trip"
		}
		if n.Subject == "" {
			return invalid("this notification is not about a trip")
		}
		if err := s.UpdateTrip(ctx, n.Subject, in); err != nil {
			return err
		}
		ev.Summary = fmt.Sprintf("%s pressed on %q: %s", title, n.Title, strings.ToLower(note[:1])+note[1:])
		s.record(ctx, ev)
		if err := s.resolve(ctx, &n, note, settings); err != nil {
			return err
		}
		if verb == model.VerbSkip {
			return s.confirmSkip(ctx, n, settings)
		}
		return nil
	}
	return invalid("unknown button %q", verb)
}

// confirmSkip tells the phone what the planner aims for after a skipped
// trip.
func (s *Service) confirmSkip(ctx context.Context, skipped model.Notification, settings model.NotificationSettings) error {
	view, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	text := "No trip needs charging now."
	if t := view.Control.Target; t != nil {
		text = fmt.Sprintf("The next target is %d%% by %s for %s.", t.SoC, s.clockText(t.ReadyBy), t.Title)
	}
	n := &model.Notification{
		Key: fmt.Sprintf("info:skip:%d", skipped.ID), Kind: model.NotifyInfo, Subject: skipped.Subject,
		Title: "Trip skipped", Message: text, Verbs: []string{model.VerbView},
	}
	if err := s.deliver(ctx, n, settings); err != nil {
		return err
	}
	n.State, n.Note = model.NotifyDone, "Information"
	_, err = s.Store.SaveNotification(*n)
	return err
}

// clockText is a time today ("10:20") or on another day ("Thu 17:00").
func (s *Service) clockText(t time.Time) string {
	l := t.In(s.loc())
	if l.Format("2006-01-02") == s.now().In(s.loc()).Format("2006-01-02") {
		return l.Format("15:04")
	}
	return l.Format("Mon 15:04")
}

// ---- listener ----

// listenRecheck is how often the button listener checks that its settings
// still apply, and waits while notifications are off.
var listenRecheck = 5 * time.Second

// ListenNotificationActions keeps a connection to Home Assistant for
// button presses while notifications are on, reconnecting after failures,
// until ctx ends.
func (s *Service) ListenNotificationActions(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		cs := s.state()
		settings, err := s.NotificationSettings()
		if err != nil || !settings.Enabled || cs.Actions == nil {
			s.setListener(nil, false)
			sleep(ctx, listenRecheck)
			continue
		}
		lctx, cancel := context.WithCancel(ctx)
		go func() {
			t := time.NewTicker(listenRecheck)
			defer t.Stop()
			for {
				select {
				case <-lctx.Done():
					return
				case <-t.C:
					n, err := s.NotificationSettings()
					if s.state().gen != cs.gen || (err == nil && !n.Enabled) {
						cancel()
						return
					}
				}
			}
		}()
		err = cs.Actions.ListenActions(lctx, func() {
			backoff = time.Second
			s.setListener(nil, true)
		}, func(id string) {
			if err := s.HandleAction(ctx, id); err != nil {
				s.Log.Warn("notification button failed", "action", id, "err", err)
			}
		})
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			// Each new problem is logged once, not on every retry.
			if prev := s.setListener(err, false); prev == nil || prev.Error() != err.Error() {
				s.Log.Warn("notification button listener", "err", err)
			}
			sleep(ctx, backoff)
			backoff = min(2*backoff, time.Minute)
			continue
		}
		s.setListener(nil, false)
	}
}

// setListener stores the button listener's state and returns its previous
// error.
func (s *Service) setListener(err error, connected bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.listenErr
	s.listenErr, s.listenOK = err, connected
	return prev
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// notificationConnection reports the phone notification status.
func (s *Service) notificationConnection() ConnectionView {
	c := ConnectionView{Name: "Phone notifications", Mode: s.Cfg.HomeAssistant.Mode}
	settings, err := s.NotificationSettings()
	s.mu.RLock()
	lerr, ok := s.listenErr, s.listenOK
	s.mu.RUnlock()
	switch {
	case err != nil:
		c.Status, c.Detail = "error", err.Error()
	case s.Clients().Notify == nil:
		c.Status, c.Detail = "not_configured", s.Clients().NotifyNote
	case !settings.Enabled:
		c.Status, c.Detail = "not_configured", "Off; turn them on under Settings → Notifications"
	case errors.Is(lerr, homeassistant.ErrNotAdmin):
		c.Status, c.Detail = "error", plural(len(settings.Phones), "phone")+"; sending works, but "+lerr.Error()+
			". Make the token's user an administrator in Home Assistant."
	case lerr != nil:
		c.Status, c.Detail = "error", plural(len(settings.Phones), "phone")+"; button presses: "+lerr.Error()
	case s.Cfg.HomeAssistant.Mode == config.ModeStub:
		c.Status, c.Detail = "stub", "Demo phone; press its buttons under Settings → Notifications"
	case ok:
		c.Status, c.Detail = "ok", plural(len(settings.Phones), "phone")+"; button presses connected"
	default:
		c.Status, c.Detail = "ok", plural(len(settings.Phones), "phone")+"; connecting for button presses"
	}
	return c
}
