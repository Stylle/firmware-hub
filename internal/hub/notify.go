package hub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type notification struct {
	Webhook          string `json:"webhook"`
	WebhookSecret    string `json:"webhook_secret"`
	TelegramToken    string `json:"telegram_token"`
	TelegramChat     string `json:"telegram_chat"`
	MQTTURL          string `json:"mqtt_url"`
	MQTTUser         string `json:"mqtt_user"`
	MQTTPassword     string `json:"mqtt_password"`
	MQTTTopic        string `json:"mqtt_topic"`
	AllowPrivate     bool   `json:"allow_private"`
	HasWebhookSecret bool   `json:"has_webhook_secret"`
	HasTelegramToken bool   `json:"has_telegram_token"`
	HasMQTTPassword  bool   `json:"has_mqtt_password"`
}

func (a *App) notification(pid int64) (notification, error) {
	var n notification
	e := a.DB.QueryRow("SELECT webhook,webhook_secret,telegram_token,telegram_chat,mqtt_url,mqtt_user,mqtt_password,mqtt_topic,allow_private FROM notifications WHERE project_id=?", pid).Scan(&n.Webhook, &n.WebhookSecret, &n.TelegramToken, &n.TelegramChat, &n.MQTTURL, &n.MQTTUser, &n.MQTTPassword, &n.MQTTTopic, &n.AllowPrivate)
	return n, e
}
func (a *App) notifications(w http.ResponseWriter, r *http.Request) {
	pid := pathID(r)
	u, ok := a.allowed(w, r, pid, false, false)
	if !ok {
		return
	}
	if a.role(u, pid) != "owner" {
		fail(w, 403, "forbidden", "仅所有者可管理通知")
		return
	}
	old, _ := a.notification(pid)
	if r.Method == "GET" {
		old.HasWebhookSecret = old.WebhookSecret != ""
		old.HasTelegramToken = old.TelegramToken != ""
		old.HasMQTTPassword = old.MQTTPassword != ""
		old.WebhookSecret = ""
		old.TelegramToken = ""
		old.MQTTPassword = ""
		jsonOut(w, 200, old)
		return
	}
	var n notification
	if readJSON(w, r, &n) != nil {
		fail(w, 400, "invalid_request", "配置格式错误")
		return
	}
	if n.AllowPrivate && !u.Admin {
		fail(w, 403, "admin_required", "内网目标仅可由平台管理员允许")
		return
	}
	if len(n.Webhook) > 2048 || len(n.TelegramChat) > 100 || len(n.MQTTTopic) > 200 || strings.ContainsAny(n.MQTTTopic, "+#\x00") {
		fail(w, 400, "invalid_notification", "通知地址或 MQTT 主题无效")
		return
	}
	if n.Webhook != "" {
		v, e := url.Parse(n.Webhook)
		if e != nil || v.User != nil || v.Hostname() == "" || (v.Scheme != "https" && !(n.AllowPrivate && v.Scheme == "http")) {
			fail(w, 400, "invalid_webhook", "Webhook 使用 HTTPS；管理员可允许内网 HTTP")
			return
		}
	}
	if n.MQTTURL != "" {
		v, e := url.Parse(n.MQTTURL)
		if e != nil || v.Hostname() == "" || v.User != nil || v.RawQuery != "" || (v.Scheme != "ssl" && !(n.AllowPrivate && v.Scheme == "tcp")) || n.MQTTTopic == "" {
			fail(w, 400, "invalid_mqtt", "MQTT 使用 ssl://host:8883；管理员可允许内网 tcp://host:1883，并填写主题")
			return
		}
	}
	for _, v := range []*string{&n.WebhookSecret, &n.TelegramToken, &n.MQTTPassword} {
		if len(*v) > 1024 {
			fail(w, 400, "invalid_secret", "凭据过长")
			return
		}
	}
	if n.WebhookSecret == "" {
		n.WebhookSecret = old.WebhookSecret
	} else if n.WebhookSecret == "-" {
		n.WebhookSecret = ""
	} else {
		n.WebhookSecret = a.seal(n.WebhookSecret)
	}
	if n.TelegramToken == "" {
		n.TelegramToken = old.TelegramToken
	} else if n.TelegramToken == "-" {
		n.TelegramToken = ""
	} else {
		n.TelegramToken = a.seal(n.TelegramToken)
	}
	if n.MQTTPassword == "" {
		n.MQTTPassword = old.MQTTPassword
	} else if n.MQTTPassword == "-" {
		n.MQTTPassword = ""
	} else {
		n.MQTTPassword = a.seal(n.MQTTPassword)
	}
	_, e := a.DB.Exec("INSERT INTO notifications(project_id,webhook,webhook_secret,telegram_token,telegram_chat,mqtt_url,mqtt_user,mqtt_password,mqtt_topic,allow_private) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(project_id) DO UPDATE SET webhook=excluded.webhook,webhook_secret=excluded.webhook_secret,telegram_token=excluded.telegram_token,telegram_chat=excluded.telegram_chat,mqtt_url=excluded.mqtt_url,mqtt_user=excluded.mqtt_user,mqtt_password=excluded.mqtt_password,mqtt_topic=excluded.mqtt_topic,allow_private=excluded.allow_private", pid, n.Webhook, n.WebhookSecret, n.TelegramToken, n.TelegramChat, n.MQTTURL, n.MQTTUser, n.MQTTPassword, n.MQTTTopic, n.AllowPrivate)
	if e != nil {
		fail(w, 500, "database_error", "保存失败")
		return
	}
	a.auditProject(u.ID, pid, "notification.update")
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func safeIPs(ctx context.Context, host string, private bool) ([]net.IPAddr, error) {
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil || len(ips) == 0 {
		return nil, errors.New("目标地址解析失败")
	}
	for _, v := range ips {
		ip := v.IP
		if !private && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
			return nil, errors.New("目标是内网或特殊地址，需要管理员明确允许")
		}
	}
	return ips, nil
}
func safeHTTP(private bool) *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("通知端点不接受重定向") }, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := safeIPs(ctx, host, private)
		if e != nil {
			return nil, e
		}
		var last error
		for _, ip := range ips {
			c, e := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return c, nil
			}
			last = e
		}
		return nil, last
	}}}
}

// RunNotifications processes a durable at-least-once outbox, one item at a time.
// Consumers deduplicate by event_id. Withdrawal cancels queued, not delivered, copies.
func (a *App) RunNotifications(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.deliverOne(ctx)
		}
	}
}
func (a *App) deliverOne(ctx context.Context) {
	var id, pid, rid int64
	var kind, event string
	var attempts int
	if a.DB.QueryRow("SELECT id,project_id,release_id,kind,event,attempts FROM outbox WHERE state='pending' AND next_at<=? ORDER BY id LIMIT 1", now()).Scan(&id, &pid, &rid, &kind, &event, &attempts) != nil {
		return
	}
	v, e := scanRelease(a.DB.QueryRow("SELECT "+releaseCols+" FROM releases WHERE id=? AND published=1", rid))
	if e != nil {
		_, _ = a.DB.Exec("UPDATE outbox SET state='cancelled',error='release unavailable' WHERE id=?", id)
		return
	}
	p, e := a.project(pid)
	if e != nil {
		return
	}
	n, e := a.notification(pid)
	if e != nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"event_id": event, "event": "release.published", "project": p.Slug, "version": v.Version, "sequence": v.Sequence, "check_path": "/api/v1/update/" + p.Slug + "/check", "created": now()})
	switch kind {
	case "webhook":
		if n.Webhook == "" {
			e = errors.New("渠道已关闭")
			break
		}
		secret, err := a.unseal(n.WebhookSecret)
		if err != nil {
			e = err
			break
		}
		req, err := http.NewRequestWithContext(ctx, "POST", n.Webhook, bytes.NewReader(payload))
		if err != nil {
			e = errors.New("Webhook 地址无效")
			break
		}
		stamp := strconv.FormatInt(now(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(stamp + "." + event + "."))
		mac.Write(payload)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-FH-Timestamp", stamp)
		req.Header.Set("X-FH-Event", event)
		req.Header.Set("X-FH-Signature", hex.EncodeToString(mac.Sum(nil)))
		e = postResult(safeHTTP(n.AllowPrivate), req)
	case "telegram":
		token, err := a.unseal(n.TelegramToken)
		if err != nil || token == "" || n.TelegramChat == "" {
			e = errors.New("Telegram 凭据或会话未配置")
			break
		}
		e = sendTelegram(ctx, safeHTTP(false), token, n.TelegramChat, p.Name+" 发布新版本 "+v.Version+"\n通道："+v.Channel)
	case "mqtt":
		e = a.sendMQTT(ctx, n, event, payload)
	default:
		e = errors.New("未知通知渠道")
	}
	if e == nil {
		_, _ = a.DB.Exec("UPDATE outbox SET state='delivered',attempts=attempts+1,error='' WHERE id=?", id)
	} else {
		state := "pending"
		if attempts >= 4 {
			state = "failed"
		}
		delay := int64(15 * (1 << attempts))
		msg := "连接失败或超时，请检查目标、证书、权限和网络"
		if strings.HasPrefix(e.Error(), "HTTP ") {
			msg = e.Error()
		}
		_, _ = a.DB.Exec("UPDATE outbox SET state=?,attempts=attempts+1,next_at=?,error=? WHERE id=?", state, now()+delay, msg, id)
	}
}
func postResult(c *http.Client, r *http.Request) error {
	defer c.CloseIdleConnections()
	resp, e := c.Do(r)
	if e != nil {
		return errors.New("通知连接失败")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
func sendTelegram(ctx context.Context, c *http.Client, token, chat, text string) error {
	defer c.CloseIdleConnections()
	body, _ := json.Marshal(map[string]any{"chat_id": chat, "text": text, "disable_web_page_preview": true})
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+url.PathEscape(token)+"/sendMessage", bytes.NewReader(body))
	if e != nil {
		return errors.New("Telegram 请求格式错误")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.Do(req)
	if e != nil {
		return errors.New("Telegram 连接失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result) != nil || !result.OK {
		return errors.New("Telegram 未确认消息成功")
	}
	return nil
}
func (a *App) sendMQTT(ctx context.Context, n notification, id string, payload []byte) error {
	u, e := url.Parse(n.MQTTURL)
	if e != nil || u.Hostname() == "" {
		return errors.New("MQTT 未配置")
	}
	ips, e := safeIPs(ctx, u.Hostname(), n.AllowPrivate)
	if e != nil {
		return e
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "ssl" {
			port = "8883"
		} else {
			port = "1883"
		}
	}
	u.Host = net.JoinHostPort(ips[0].IP.String(), port)
	password, e := a.unseal(n.MQTTPassword)
	if e != nil {
		return e
	}
	opts := mqtt.NewClientOptions().AddBroker(u.String()).SetClientID("fh-" + id[:16]).SetUsername(n.MQTTUser).SetPassword(password).SetConnectTimeout(8 * time.Second).SetAutoReconnect(false).SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: host})
	c := mqtt.NewClient(opts)
	t := c.Connect()
	if !t.WaitTimeout(10 * time.Second) {
		c.Disconnect(0)
		return errors.New("MQTT 超时")
	}
	if t.Error() != nil {
		return errors.New("MQTT 连接失败")
	}
	defer c.Disconnect(100)
	t = c.Publish(n.MQTTTopic, 1, false, payload)
	if !t.WaitTimeout(10 * time.Second) {
		return errors.New("MQTT 发布超时")
	}
	return t.Error()
}
func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	pid := pathID(r)
	_, ok := a.allowed(w, r, pid, false, false)
	if !ok {
		return
	}
	rows, e := a.DB.Query("SELECT id,release_id,kind,state,attempts,error,created FROM outbox WHERE project_id=? ORDER BY id DESC LIMIT 100", pid)
	if e != nil {
		fail(w, 500, "database_error", "读取失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, rid, created int64
		var kind, state, msg string
		var attempts int
		_ = rows.Scan(&id, &rid, &kind, &state, &attempts, &msg, &created)
		out = append(out, map[string]any{"id": id, "release_id": rid, "kind": kind, "state": state, "attempts": attempts, "error": msg, "created": created})
	}
	jsonOut(w, 200, out)
}
