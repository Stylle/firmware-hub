package hub

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type mockTransport func(*http.Request) (*http.Response, error)

func (m mockTransport) RoundTrip(r *http.Request) (*http.Response, error) { return m(r) }
func TestTelegramRequestAndAPIAcknowledgement(t *testing.T) {
	for _, ok := range []bool{true, false} {
		client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Scheme != "https" || r.URL.Host != "api.telegram.org" || !strings.HasSuffix(r.URL.Path, "/sendMessage") {
				t.Fatal("unsafe Telegram destination")
			}
			data, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(data), `"chat_id":"test-chat"`) || strings.Contains(string(data), "test-token") {
				t.Fatal("invalid or secret-leaking message body")
			}
			body := `{"ok":false}`
			if ok {
				body = `{"ok":true}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
		e := sendTelegram(context.Background(), client, "test-token", "test-chat", "Release published")
		if (e == nil) != ok {
			t.Fatal("API acknowledgement not enforced")
		}
	}
}
