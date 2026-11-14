package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	probe "github.com/Stylle/firmware-hub/internal/integrationtest"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (a *App) testNotification(w http.ResponseWriter, r *http.Request) {
	pid := pathID(r)
	u, ok := a.allowed(w, r, pid, false, false)
	if !ok {
		return
	}
	if a.role(u, pid) != "owner" {
		fail(w, 403, "forbidden", "仅所有者可测试通知")
		return
	}
	var in struct {
		Channel string       `json:"channel"`
		Action  string       `json:"action"`
		Config  notification `json:"config"`
	}
	if readJSON(w, r, &in) != nil {
		fail(w, 400, "invalid_request", "测试配置格式错误")
		return
	}
	n := in.Config
	if n.AllowPrivate && !u.Admin {
		fail(w, 403, "admin_required", "内网目标仅可由平台管理员允许")
		return
	}
	old, e := notificationRaw(a.DB, pid)
	if e != nil {
		fail(w, 500, "database_error", "读取配置失败")
		return
	}
	if in.Channel != "webhook" && in.Channel != "mqtt" {
		fail(w, 400, "invalid_channel", "未知通知渠道")
		return
	}
	topicOverride := n.MQTTTopic
	source := sourceFor(n, in.Channel)
	if source != 0 {
		var same bool
		e = a.DB.QueryRow("SELECT a.owner=b.owner FROM projects a JOIN projects b ON b.id=? WHERE a.id=?", source, pid).Scan(&same)
		if e != nil || !same {
			fail(w, 403, "forbidden", "仅可共用同一所有者项目的渠道")
			return
		}
		n, e = resolveNotice(a.DB, source, in.Channel, map[int64]bool{pid: true})
		if e != nil {
			fail(w, 400, "invalid_source", e.Error())
			return
		}
		if in.Channel == "mqtt" && topicOverride != "" {
			n.MQTTTopic = topicOverride
		}
		old = n
		n.WebhookSecret = ""
		n.TelegramToken = ""
		n.MQTTPassword = ""
	}
	secret := func(draft, saved string) (string, error) {
		if draft == "-" {
			return "", nil
		}
		if draft != "" {
			return draft, nil
		}
		return a.unseal(saved)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result any
	switch in.Channel {
	case "webhook":
		v, err := url.Parse(n.Webhook)
		if err != nil || v.User != nil || v.Hostname() == "" || (v.Scheme != "https" && !(n.AllowPrivate && v.Scheme == "http")) {
			e = errors.New("Webhook 使用 HTTPS；管理员可允许内网 HTTP")
			break
		}
		key, err := secret(n.WebhookSecret, old.WebhookSecret)
		if err != nil {
			e = errors.New("Webhook 密钥无法读取")
			break
		}
		event := randomToken()
		stamp := strconv.FormatInt(now(), 10)
		payload, _ := json.Marshal(map[string]any{"event": "notification.test", "event_id": event, "project_id": pid, "message": "Firmware Hub · 当前表单通知测试（未保存配置）"})
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(stamp + "." + event + "."))
		mac.Write(payload)
		_, e = probe.Webhook(ctx, safeHTTP(n.AllowPrivate), "POST", n.Webhook, map[string]string{"Content-Type": "application/json", "X-FH-Timestamp": stamp, "X-FH-Event": event, "X-FH-Signature": hex.EncodeToString(mac.Sum(nil))}, payload)
	case "telegram":
		token, err := secret(n.TelegramToken, old.TelegramToken)
		if err != nil {
			e = errors.New("Telegram 密钥无法读取")
			break
		}
		client := probe.Telegram{HTTP: safeHTTP(false)}
		if in.Action == "detect" {
			var bot probe.Bot
			bot, e = client.Check(ctx, token)
			if e == nil {
				var chats []string
				chats, e = client.Discover(ctx, token)
				result = map[string]any{"username": bot.Username, "chats": chats}
			}
		} else {
			e = client.Send(ctx, token, n.TelegramChat, "Firmware Hub · 当前表单 Telegram 测试\n不会保存或启用通知配置。")
		}
	case "mqtt":
		n.MQTTPassword, e = secret(n.MQTTPassword, old.MQTTPassword)
		if e != nil {
			e = errors.New("MQTT 密钥无法读取")
			break
		}
		n.MQTTPassword = a.seal(n.MQTTPassword)
		var slug string
		a.DB.QueryRow("SELECT slug FROM projects WHERE id=?", pid).Scan(&slug)
		n.MQTTTopic = strings.ReplaceAll(n.MQTTTopic, "{project}", slug)
		e = a.sendMQTT(ctx, n, randomToken(), []byte(`{"event":"notification.test"}`))
	}
	if e != nil {
		fail(w, 502, "test_failed", e.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "persisted": false, "result": result})
}
