package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Notifier sends phone notifications through the Home Assistant Companion
// app (notify.mobile_app_* services).
type Notifier interface {
	// Phones lists the mobile app notify services, e.g. mobile_app_phone.
	Phones(ctx context.Context) ([]string, error)
	Notify(ctx context.Context, phone string, m Message) error
	// Clear removes the notification with tag from the phone.
	Clear(ctx context.Context, phone, tag string) error
}

// ActionListener delivers the buttons pressed on notifications.
type ActionListener interface {
	// ListenActions calls ready once subscribed, then action with the ID of
	// each button pressed, until ctx ends or the connection fails.
	ListenActions(ctx context.Context, ready func(), action func(id string)) error
}

// Message is a phone notification.
type Message struct {
	Title   string
	Text    string
	Tag     string // a newer message with the same tag replaces it
	Actions []Action
	Link    string // opened when the notification is tapped
	Urgent  bool   // time-sensitive (iOS) and high priority (Android)
}

// Action is a notification button: an ID sent back when pressed, or a URI
// to open.
type Action struct {
	ID    string
	Title string
	URI   string
}

// ActionEvent is Home Assistant's event for a pressed button.
const ActionEvent = "mobile_app_notification_action"

// payload builds the notify service call.
func payload(m Message) map[string]any {
	data := map[string]any{"tag": m.Tag, "group": "evcc-planner"}
	if len(m.Actions) > 0 {
		var actions []map[string]string
		for _, a := range m.Actions {
			if a.URI != "" {
				actions = append(actions, map[string]string{"action": "URI", "title": a.Title, "uri": a.URI})
			} else {
				actions = append(actions, map[string]string{"action": a.ID, "title": a.Title})
			}
		}
		data["actions"] = actions
	}
	if m.Link != "" {
		data["url"], data["clickAction"] = m.Link, m.Link
	}
	if m.Urgent {
		data["push"] = map[string]any{"interruption-level": "time-sensitive"}
		data["priority"], data["ttl"] = "high", 0
	}
	return map[string]any{"title": m.Title, "message": m.Text, "data": data}
}

// Phones lists the mobile app notify services.
func (c *HTTPClient) Phones(ctx context.Context) ([]string, error) {
	body, err := c.get(ctx, "/api/services")
	if err != nil {
		return nil, err
	}
	var domains []struct {
		Domain   string                     `json:"domain"`
		Services map[string]json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(body, &domains); err != nil {
		return nil, fmt.Errorf("decode services: %w", err)
	}
	out := []string{}
	for _, d := range domains {
		if d.Domain != "notify" {
			continue
		}
		for name := range d.Services {
			if strings.HasPrefix(name, "mobile_app_") {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Notify calls notify.<phone>.
func (c *HTTPClient) Notify(ctx context.Context, phone string, m Message) error {
	return c.post(ctx, "/api/services/notify/"+phone, payload(m))
}

// Clear removes a notification by its tag.
func (c *HTTPClient) Clear(ctx context.Context, phone, tag string) error {
	return c.post(ctx, "/api/services/notify/"+phone,
		map[string]any{"message": "clear_notification", "data": map[string]string{"tag": tag}})
}

func (c *HTTPClient) post(ctx context.Context, path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("home assistant unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return apiError(resp.StatusCode, body)
	}
	return nil
}

// wsPing is how often the listener pings Home Assistant, so an idle
// connection is not dropped and a dead one is noticed.
var wsPing = 30 * time.Second

// ListenActions subscribes to button presses over Home Assistant's
// websocket API with the access token. Home Assistant never has to reach
// the planner.
func (c *HTTPClient) ListenActions(ctx context.Context, ready func(), action func(string)) error {
	u := strings.TrimRight(c.BaseURL, "/") + "/api/websocket"
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dialCtx, u, nil)
	cancel()
	if err != nil {
		return fmt.Errorf("home assistant websocket: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	var msg struct {
		ID      int    `json:"id"`
		Type    string `json:"type"`
		Success bool   `json:"success"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
		Event struct {
			EventType string         `json:"event_type"`
			Data      map[string]any `json:"data"`
		} `json:"event"`
	}
	read := func() error {
		msg.Type, msg.Error, msg.Event.Data = "", nil, nil
		return wsjson.Read(ctx, conn, &msg)
	}
	if err := read(); err != nil {
		return fmt.Errorf("home assistant websocket: %w", err)
	}
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "auth", "access_token": c.Token}); err != nil {
		return err
	}
	if err := read(); err != nil {
		return fmt.Errorf("home assistant websocket: %w", err)
	}
	if msg.Type != "auth_ok" {
		return errors.New("home assistant websocket: access token rejected")
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"id": 1, "type": "subscribe_events", "event_type": ActionEvent}); err != nil {
		return err
	}
	if err := read(); err != nil {
		return fmt.Errorf("home assistant websocket: %w", err)
	}
	if msg.Type != "result" || !msg.Success {
		reason := "subscription refused"
		if msg.Error != nil {
			reason = msg.Error.Message
		}
		return fmt.Errorf("home assistant websocket: %s", reason)
	}
	ready()

	pingCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		t := time.NewTicker(wsPing)
		defer t.Stop()
		for id := 2; ; id++ {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				if wsjson.Write(pingCtx, conn, map[string]any{"id": id, "type": "ping"}) != nil {
					conn.CloseNow()
					return
				}
			}
		}
	}()
	for {
		if err := read(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("home assistant websocket: %w", err)
		}
		if msg.Type == "event" && msg.Event.EventType == ActionEvent {
			if id, _ := msg.Event.Data["action"].(string); id != "" {
				action(id)
			}
		}
	}
}

// ---- stub ----

// StubPhone is the phone the notification stub offers.
const StubPhone = "mobile_app_demo_phone"

// Sent is a message the stub received.
type Sent struct {
	Phone   string
	Message Message
	Cleared bool // a clear_notification for Message.Tag
}

// NotifyStub records notifications instead of sending them (demo mode and
// tests).
type NotifyStub struct {
	mu   sync.Mutex
	sent []Sent
	// Fail, if set, makes every call fail with it.
	Fail error
}

// Phones lists the demo phone.
func (s *NotifyStub) Phones(context.Context) ([]string, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	return []string{StubPhone}, nil
}

// Notify records a message.
func (s *NotifyStub) Notify(_ context.Context, phone string, m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return s.Fail
	}
	s.sent = append(s.sent, Sent{Phone: phone, Message: m})
	return nil
}

// Clear records a clear_notification.
func (s *NotifyStub) Clear(_ context.Context, phone, tag string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return s.Fail
	}
	s.sent = append(s.sent, Sent{Phone: phone, Message: Message{Tag: tag}, Cleared: true})
	return nil
}

// Take returns the recorded calls and forgets them.
func (s *NotifyStub) Take() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.sent
	s.sent = nil
	return out
}
