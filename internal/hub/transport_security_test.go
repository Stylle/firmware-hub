package hub

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
	fail      bool
}

func (w *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	w.deadlines = append(w.deadlines, t)
	if w.fail {
		return errors.New("fixture deadline failure")
	}
	return nil
}
func TestDownloadWriteDeadlineRenewsAndFailsClosed(t *testing.T) {
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	stream := idleDownloadWriter{w, time.Second}
	if _, e := stream.Write([]byte("one")); e != nil {
		t.Fatal(e)
	}
	if _, e := stream.Write([]byte("two")); e != nil {
		t.Fatal(e)
	}
	if len(w.deadlines) != 2 || w.deadlines[1].Before(w.deadlines[0]) || w.deadlines[1].Before(time.Now()) {
		t.Fatal("idle deadline not renewed")
	}
	w.fail = true
	if _, e := stream.Write([]byte("not written")); e == nil || w.Body.String() != "onetwo" {
		t.Fatal("deadline failure ignored")
	}
}
func TestProxyIdentityCannotBeForged(t *testing.T) {
	r := httptest.NewRequest("POST", "http://hub.test/api/v1/session", nil)
	r.RemoteAddr = "192.0.2.10:123"
	r.Header.Set("X-Real-IP", "198.51.100.1")
	r.Header.Set("X-Forwarded-Proto", "https")
	if requestPeer(r) != "192.0.2.10" || requestHTTPS(r) {
		t.Fatal("untrusted forwarding accepted")
	}
	t.Setenv("TRUSTED_PROXIES", "192.0.2.0/24")
	if requestPeer(r) != "198.51.100.1" || !requestHTTPS(r) {
		t.Fatal("trusted forwarding ignored")
	}
	limiter := newLimiter()
	for i := 0; i < 12; i++ {
		limiter.allow(requestPeer(r))
	}
	if limiter.allow(requestPeer(r)) {
		t.Fatal("first client unlimited")
	}
	r.Header.Set("X-Real-IP", "198.51.100.2")
	if !limiter.allow(requestPeer(r)) {
		t.Fatal("proxy clients share lockout")
	}
}
