package hub

import (
	"database/sql"
	"errors"
	"strings"
)

type noticeQuery interface{ QueryRow(string, ...any) *sql.Row }

func noticeEnabled(value *bool) bool { return value == nil || *value }

// Local credentials stay with their project. A reference only reuses the selected
// connection, never its project's recipient-independent enable/disable switch.
func notificationRaw(q noticeQuery, pid int64) (notification, error) {
	n := notification{}
	err := q.QueryRow("SELECT webhook,webhook_secret,telegram_token,telegram_chat,mqtt_url,mqtt_user,mqtt_password,mqtt_topic,allow_private FROM notifications WHERE project_id=?", pid).Scan(&n.Webhook, &n.WebhookSecret, &n.TelegramToken, &n.TelegramChat, &n.MQTTURL, &n.MQTTUser, &n.MQTTPassword, &n.MQTTTopic, &n.AllowPrivate)
	if err != nil && err != sql.ErrNoRows {
		return n, err
	}
	hook, tg, mqtt := true, true, true
	err = q.QueryRow("SELECT webhook_enabled,telegram_enabled,mqtt_enabled,webhook_source,telegram_source,mqtt_source FROM notification_options WHERE project_id=?", pid).Scan(&hook, &tg, &mqtt, &n.WebhookSource, &n.TelegramSource, &n.MQTTSource)
	n.WebhookEnabled, n.TelegramEnabled, n.MQTTEnabled = &hook, &tg, &mqtt
	if err == sql.ErrNoRows {
		err = nil
	}
	return n, err
}

func sourceFor(n notification, kind string) int64 {
	switch kind {
	case "webhook":
		return n.WebhookSource
	case "telegram":
		return n.TelegramSource
	default:
		return n.MQTTSource
	}
}

func resolveNotice(q noticeQuery, pid int64, kind string, seen map[int64]bool) (notification, error) {
	if seen[pid] || len(seen) >= 16 {
		return notification{}, errors.New("共用渠道不能循环引用或超过16层")
	}
	seen[pid] = true
	n, err := notificationRaw(q, pid)
	if err != nil {
		return n, err
	}
	source := sourceFor(n, kind)
	if source == 0 {
		return n, nil
	}
	var sameOwner bool
	if err = q.QueryRow("SELECT a.owner=b.owner FROM projects a JOIN projects b ON b.id=? WHERE a.id=?", source, pid).Scan(&sameOwner); err != nil || !sameOwner {
		return n, errors.New("仅可共用同一所有者项目的通知渠道")
	}
	shared, err := resolveNotice(q, source, kind, seen)
	if err != nil {
		return n, err
	}
	// A recipient may override the MQTT topic while sharing its broker credentials.
	if kind == "mqtt" && n.MQTTTopic != "" {
		shared.MQTTTopic = n.MQTTTopic
	}
	return shared, nil
}

func notificationFor(q noticeQuery, pid int64) (notification, error) {
	n, err := notificationRaw(q, pid)
	if err != nil {
		return n, err
	}
	for _, kind := range []string{"webhook", "telegram", "mqtt"} {
		shared, err := resolveNotice(q, pid, kind, map[int64]bool{})
		if err != nil {
			return n, err
		}
		switch kind {
		case "webhook":
			n.Webhook, n.WebhookSecret, n.webhookPrivate = shared.Webhook, shared.WebhookSecret, shared.AllowPrivate
		case "telegram":
			n.TelegramToken, n.TelegramChat = shared.TelegramToken, shared.TelegramChat
		case "mqtt":
			n.MQTTURL, n.MQTTUser, n.MQTTPassword, n.MQTTTopic, n.mqttPrivate = shared.MQTTURL, shared.MQTTUser, shared.MQTTPassword, shared.MQTTTopic, shared.AllowPrivate
		}
	}
	var slug string
	if err = q.QueryRow("SELECT slug FROM projects WHERE id=?", pid).Scan(&slug); err != nil {
		return n, err
	}
	n.MQTTTopic = strings.ReplaceAll(n.MQTTTopic, "{project}", slug)
	if !noticeEnabled(n.WebhookEnabled) {
		n.Webhook = ""
	}
	if !noticeEnabled(n.TelegramEnabled) {
		n.TelegramToken = ""
	}
	if !noticeEnabled(n.MQTTEnabled) {
		n.MQTTURL = ""
	}
	return n, nil
}
