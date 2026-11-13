package hub

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSharedChannelsSwitchesTopicAndCredentials(t *testing.T) {
	f := setup(t)
	r, err := f.a.DB.Exec("INSERT INTO projects(slug,name,owner,pubkey) SELECT 'second','Second',owner,'' FROM projects WHERE id=?", f.pid)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := r.LastInsertId()
	path := func(id int64) string { return fmt.Sprintf("/api/v1/projects/%d/notifications", id) }
	save := func(id int64, body any) {
		t.Helper()
		w := f.call("PUT", path(id), body, true, "")
		if w.Code != 200 {
			t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
		}
	}
	save(f.pid, map[string]any{"webhook": "https://example.com/hook", "webhook_secret": "test-shared-signing-key", "mqtt_url": "ssl://example.com:8883", "mqtt_topic": "firmware/{project}/available", "mqtt_password": "test-mqtt-password", "webhook_enabled": false})
	save(second, map[string]any{"webhook_source": f.pid, "mqtt_source": f.pid, "webhook_enabled": true, "mqtt_enabled": true})
	n, err := f.a.notification(second)
	if err != nil || n.Webhook != "https://example.com/hook" || n.MQTTTopic != "firmware/second/available" {
		t.Fatalf("shared route mismatch: %v", err)
	}
	plain, err := f.a.unseal(n.WebhookSecret)
	if err != nil || plain != "test-shared-signing-key" {
		t.Fatal("shared encrypted secret unavailable")
	}
	source, _ := f.a.notification(f.pid)
	if source.Webhook != "" {
		t.Fatal("source switch not independent")
	}
	got := f.call("GET", path(second), nil, true, "")
	if strings.Contains(got.Body.String(), "test-shared-signing-key") || strings.Contains(got.Body.String(), "test-mqtt-password") || strings.Contains(got.Body.String(), n.WebhookSecret) {
		t.Fatal("referenced credentials exposed")
	}
	save(second, map[string]any{"webhook_source": f.pid, "mqtt_source": f.pid, "webhook_enabled": false, "mqtt_enabled": true, "mqtt_topic": "custom/{project}"})
	n, err = f.a.notification(second)
	if err != nil || n.Webhook != "" || n.MQTTTopic != "custom/second" {
		t.Fatal("disabled route or topic override broken")
	}
	// Re-enable without re-entering any credentials.
	save(second, map[string]any{"webhook_source": f.pid, "mqtt_source": f.pid, "webhook_enabled": true})
	n, _ = f.a.notification(second)
	if n.Webhook == "" {
		t.Fatal("configuration lost on disable")
	}
	cycle := f.call("PUT", path(f.pid), map[string]any{"webhook_source": second}, true, "")
	if cycle.Code != 400 {
		t.Fatal("cycle accepted")
	}
	source, _ = notificationRaw(f.a.DB, f.pid)
	if source.Webhook != "https://example.com/hook" || source.WebhookSource != 0 {
		t.Fatal("failed save was not rolled back")
	}
	// Sharing is bounded by actual ownership, even for a platform administrator.
	if _, err = f.a.DB.Exec("INSERT INTO users(name,password) VALUES('other-owner','unused')"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.a.DB.Exec("UPDATE projects SET owner=(SELECT id FROM users WHERE name='other-owner') WHERE id=?", second); err != nil {
		t.Fatal(err)
	}
	denied := f.call("PUT", path(f.pid), map[string]any{"mqtt_source": second}, true, "")
	if denied.Code != 400 {
		t.Fatal("cross-owner sharing accepted")
	}
}

func TestMFAChallengeOnlyAfterCorrectPassword(t *testing.T) {
	f := setup(t)
	var uid int64
	f.a.DB.QueryRow("SELECT id FROM users WHERE name='admin'").Scan(&uid)
	if _, err := f.a.DB.Exec("INSERT INTO mfa(user_id,secret) VALUES(?,?)", uid, f.a.seal("TESTSEED")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ password, want string }{{"wrong", "invalid_login"}, {"TestOnly-Password-1234", "mfa_required"}} {
		w := f.call("POST", "/api/v1/session", map[string]any{"name": "admin", "password": tc.password}, false, "")
		var payload map[string]any
		json.Unmarshal(w.Body.Bytes(), &payload)
		if w.Code != 401 || payload["error"] != tc.want || len(w.Result().Cookies()) != 0 {
			t.Fatal("MFA challenge leaked account state or established session")
		}
	}
}
