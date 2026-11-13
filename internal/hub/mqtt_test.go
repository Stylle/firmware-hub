package hub

import (
	"context"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"os"
	"testing"
	"time"
)

func TestMQTTBrokerRoundTrip(t *testing.T) {
	broker := os.Getenv("FH_MQTT_TEST_URL")
	if broker == "" {
		t.Skip("set FH_MQTT_TEST_URL to a dedicated local broker for real integration")
	}
	f := setup(t)
	topic := "firmware-hub-test/" + randomToken()
	got := make(chan string, 1)
	c := mqtt.NewClient(mqtt.NewClientOptions().AddBroker(broker).SetClientID("test-" + randomToken()[:12]).SetConnectTimeout(5 * time.Second))
	token := c.Connect()
	if !token.WaitTimeout(6*time.Second) || token.Error() != nil {
		t.Fatal("broker connect failed")
	}
	defer c.Disconnect(100)
	token = c.Subscribe(topic, 1, func(_ mqtt.Client, m mqtt.Message) {
		select {
		case got <- string(m.Payload()):
		default:
		}
	})
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("subscribe failed")
	}
	if e := f.a.sendMQTT(context.Background(), notification{MQTTURL: broker, MQTTTopic: topic, AllowPrivate: true}, randomToken(), []byte(`{"event_id":"test","event":"release.published"}`)); e != nil {
		t.Fatal(e)
	}
	select {
	case msg := <-got:
		if msg != `{"event_id":"test","event":"release.published"}` {
			t.Fatal(msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("message not received")
	}
}
