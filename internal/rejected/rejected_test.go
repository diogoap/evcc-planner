package rejected

import (
	"errors"
	"fmt"
	"testing"
)

func TestRefusalsAreRecognisedAlsoAsText(t *testing.T) {
	key := &Error{What: "API key", Status: 401, Message: "No auth credentials found"}
	wrapped := fmt.Errorf("openrouter: %w", key)
	if wrapped.Error() != "openrouter: API key rejected (HTTP 401): No auth credentials found" {
		t.Errorf("text = %q", wrapped)
	}
	if !Is(wrapped) || !In(wrapped.Error()) || !In("Work calendar: "+wrapped.Error()) {
		t.Error("a wrapped refusal is not recognised")
	}
	socket := &Error{What: "access token"}
	if socket.Error() != "access token rejected" || !Is(socket) || In(socket.Error()) {
		t.Errorf("without a status = %q", socket)
	}
	plain := errors.New("routes api: quota exceeded")
	if Is(plain) || In(plain.Error()) || In("access token rejected") {
		t.Error("other errors count as refusals")
	}
}
