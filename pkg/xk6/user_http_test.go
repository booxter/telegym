package telegym

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSendInjectsTextUpdate pins the payload shape telegym-mock expects on
// /debug/inject/update for a plain text message.
func TestSendInjectsTextUpdate(t *testing.T) {
	m, c := newFakeMock(t)
	u := c.NewUser(777, nil)
	u.Username, u.FirstName = "bob", "Bob"

	if err := u.Send("/start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := m.lastInjected()
	want := map[string]any{
		"token":      "42:test",
		"user_id":    float64(777),
		"chat_id":    float64(777),
		"username":   "bob",
		"first_name": "Bob",
		"text":       "/start",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("inject[%q] = %v, want %v", k, got[k], v)
		}
	}
}

// TestClickTargetsLastMatchedMessage is the core click contract: the
// callback query must reference the message the scenario just awaited,
// otherwise the bot edits or answers the wrong message.
func TestClickTargetsLastMatchedMessage(t *testing.T) {
	m, c := newFakeMock(t)
	u := c.NewUser(1, nil)
	m.setMessages(Message{MessageID: 55, Text: "pick", EventSeq: 1, ReplyMarkup: buttons("age:18")})

	msg, err := u.AwaitButton("^age:", 1)
	if err != nil {
		t.Fatalf("AwaitButton: %v", err)
	}
	if err := msg.FindButton("^age:18$").Click(u); err != nil {
		t.Fatalf("Button.Click: %v", err)
	}

	got := m.lastInjected()
	if got["callback_data"] != "age:18" {
		t.Errorf("callback_data = %v, want age:18", got["callback_data"])
	}
	if got["message_id"] != float64(55) {
		t.Errorf("message_id = %v, want 55", got["message_id"])
	}
}

func TestInjectErrors(t *testing.T) {
	t.Run("non-2xx status", func(t *testing.T) {
		m, c := newFakeMock(t)
		m.injectStatus = http.StatusBadRequest
		err := c.NewUser(1, nil).Send("hi")
		if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "boom") {
			t.Errorf("Send error = %v, want status 400 with response body", err)
		}
	})
	t.Run("mock unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		t.Setenv("TELEGYM_MOCK_URL", srv.URL)
		if err := NewClient().NewUser(1, nil).Send("hi"); err == nil {
			t.Error("Send to a closed server returned nil error")
		}
	})
}

// TestAwaitTextMatchesTextAndCaption: media replies carry their text in
// caption, and scenarios should not care which field the bot used.
func TestAwaitTextMatchesTextAndCaption(t *testing.T) {
	m, c := newFakeMock(t)
	u := c.NewUser(9, nil)
	m.setMessages(
		Message{MessageID: 1, Text: "unrelated", EventSeq: 1, Date: 100},
		Message{MessageID: 2, Caption: "Welcome, Bob!", EventSeq: 2, Date: 101},
	)

	msg, err := u.AwaitText(`(?i)welcome`, 1)
	if err != nil {
		t.Fatalf("AwaitText: %v", err)
	}
	if msg.MessageID != 2 {
		t.Errorf("matched MessageID = %d, want 2", msg.MessageID)
	}
	if msg.LatencyMS < 0 {
		t.Errorf("LatencyMS = %v, want >= 0", msg.LatencyMS)
	}
	if msg.user != u {
		t.Error("returned message is not bound to the user")
	}

	// The poll query identifies the chat and starts from the user's floor.
	q := m.lastQuery()
	if !strings.HasPrefix(q, "/debug/messages/42:test?") || !strings.Contains(q, "chat_id=9") {
		t.Errorf("poll query = %q", q)
	}

	// After a match the floor moves to the matched message date.
	if u.since != 101 {
		t.Errorf("since = %d, want 101", u.since)
	}
	if _, err := u.AwaitText("unrelated", 0.05); err == nil {
		t.Error("AwaitText matched a message older than the previous match")
	}
	if q := m.lastQuery(); !strings.Contains(q, "since=101") {
		t.Errorf("poll query after match = %q, want since=101", q)
	}
}

// TestAwaitDedupesByEventSeq covers bot edits: an editMessage produces a
// second entry with the same MessageID but a higher EventSeq. Each await
// must return each entry once, in order, and never the same one twice.
func TestAwaitDedupesByEventSeq(t *testing.T) {
	m, c := newFakeMock(t)
	u := c.NewUser(1, nil)
	m.setMessages(
		Message{MessageID: 10, Text: "step 1", EventSeq: 1},
		Message{MessageID: 10, Text: "step 2", EventSeq: 2},
	)

	for _, want := range []string{"step 1", "step 2"} {
		msg, err := u.AwaitText("^step", 1)
		if err != nil {
			t.Fatalf("AwaitText(%q): %v", want, err)
		}
		if msg.Text != want {
			t.Errorf("got %q, want %q", msg.Text, want)
		}
	}
	if _, err := u.AwaitText("^step", 0.05); err == nil {
		t.Error("third AwaitText returned an already-seen message")
	}
}

func TestAwaitButton(t *testing.T) {
	m, c := newFakeMock(t)
	u := c.NewUser(1, nil)
	m.setMessages(
		Message{MessageID: 1, Text: "no keyboard", EventSeq: 1},
		Message{MessageID: 2, Text: "menu", EventSeq: 2, ReplyMarkup: buttons("help", "country:UA")},
	)

	msg, err := u.AwaitButton("^country:", 1)
	if err != nil {
		t.Fatalf("AwaitButton: %v", err)
	}
	if msg.MessageID != 2 {
		t.Errorf("matched MessageID = %d, want 2", msg.MessageID)
	}
	if _, err := u.AwaitButton("^missing$", 0.05); err == nil {
		t.Error("AwaitButton matched a callback_data that is not on any keyboard")
	}
}

func TestAwaitErrors(t *testing.T) {
	t.Run("bad pattern", func(t *testing.T) {
		_, c := newFakeMock(t)
		u := c.NewUser(1, nil)
		if _, err := u.AwaitText("(", 1); err == nil {
			t.Error("AwaitText accepted an invalid regexp")
		}
		if _, err := u.AwaitButton("(", 1); err == nil {
			t.Error("AwaitButton accepted an invalid regexp")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		_, c := newFakeMock(t)
		_, err := c.NewUser(5, nil).AwaitText("never", 0.1)
		if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "chat=5") {
			t.Errorf("err = %v, want timeout mentioning chat=5", err)
		}
	})
	t.Run("malformed response", func(t *testing.T) {
		m, c := newFakeMock(t)
		m.rawMessages = "not json"
		if _, err := c.NewUser(1, nil).AwaitText("x", 1); err == nil {
			t.Error("AwaitText ignored a malformed /debug/messages body")
		}
	})
	t.Run("mock unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		t.Setenv("TELEGYM_MOCK_URL", srv.URL)
		if _, err := NewClient().NewUser(1, nil).AwaitText("x", 1); err == nil {
			t.Error("AwaitText against a closed server returned nil error")
		}
	})
}

func TestFindButton(t *testing.T) {
	msg := &Message{ReplyMarkup: &ReplyMarkup{InlineKeyboard: [][]Button{
		{{Text: "UA", CallbackData: "country:UA"}},
		{{Text: "PL", CallbackData: "country:PL"}},
	}}}

	b := msg.FindButton("PL$")
	if b == nil || b.CallbackData != "country:PL" {
		t.Fatalf("FindButton(PL$) = %+v, want country:PL", b)
	}
	// The result is a copy: mutating it must not touch the keyboard.
	b.CallbackData = "changed"
	if msg.ReplyMarkup.InlineKeyboard[1][0].CallbackData != "country:PL" {
		t.Error("FindButton returned a pointer into the message keyboard")
	}

	for name, tc := range map[string]struct {
		msg     *Message
		pattern string
	}{
		"nil message":  {nil, "x"},
		"no keyboard":  {&Message{Text: "hi"}, "x"},
		"bad pattern":  {msg, "("},
		"no such data": {msg, "^country:DE$"},
	} {
		if got := tc.msg.FindButton(tc.pattern); got != nil {
			t.Errorf("%s: FindButton = %+v, want nil", name, got)
		}
	}
}

func TestNilButtonClick(t *testing.T) {
	var b *Button
	err := b.Click(&User{})
	if err == nil {
		t.Fatal("Click on nil button returned nil error")
	}
	if got := fmt.Sprint(err); got != "nil button" {
		t.Errorf("err = %q, want %q", got, "nil button")
	}
}
