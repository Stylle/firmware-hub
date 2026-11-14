package hub

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotificationDraftDoesNotSave(t *testing.T) {
	f := setup(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("X-FH-Signature") == "" {
			t.Error("missing signature")
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	path := fmt.Sprintf("/api/v1/projects/%d/notifications", f.pid)
	before, _ := notificationRaw(f.a.DB, f.pid)
	w := f.call("POST", path+"/test", map[string]any{"channel": "webhook", "config": notification{Webhook: srv.URL, WebhookSecret: "fixture", AllowPrivate: true}}, true, "")
	if w.Code != 200 || hits != 1 {
		t.Fatal("draft webhook did not run", w.Code)
	}
	after, _ := notificationRaw(f.a.DB, f.pid)
	if before.Webhook != after.Webhook || before.WebhookSecret != after.WebhookSecret {
		t.Fatal("draft was persisted")
	}
}

func TestTelegramChannelRemovedWithoutDeletingLegacyCredentials(t *testing.T) {
	f := setup(t)
	oldKey := f.a.seal("fixture-legacy-token")
	_, e := f.a.DB.Exec("INSERT INTO notifications(project_id,telegram_token,telegram_chat) VALUES(?,?,?)", f.pid, oldKey, "42")
	if e != nil {
		t.Fatal(e)
	}
	path := fmt.Sprintf("/api/v1/projects/%d/notifications", f.pid)
	w := f.call("PUT", path, map[string]any{"webhook": "https://example.com/hook"}, true, "")
	if w.Code != 200 {
		t.Fatal("save failed")
	}
	raw, e := notificationRaw(f.a.DB, f.pid)
	if e != nil || raw.TelegramToken != oldKey || raw.TelegramChat != "42" {
		t.Fatal("legacy data lost")
	}
	active, e := f.a.notification(f.pid)
	if e != nil || active.TelegramToken != "" || noticeEnabled(active.TelegramEnabled) {
		t.Fatal("legacy Telegram still active")
	}
	w = f.call("POST", path+"/test", map[string]any{"channel": "telegram"}, true, "")
	if w.Code != 400 {
		t.Fatal("removed channel accepted")
	}
}
