package hub

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/Stylle/firmware-hub/internal/protocol"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestHistoryPaginationAndLatestBeyond200OtherPlatforms(t *testing.T) {
	f := setup(t)
	f.published([]byte("initial x64 artifact"))
	rs, _ := f.a.listReleases(f.pid, true)
	base := rs[0]
	key := f.key.Public().(ed25519.PublicKey)
	m, e := (protocol.Envelope{Payload: base.Payload, Signature: base.Signature}).Verify(key)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.a.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 205; i++ {
		m.Sequence = int64(i + 3)
		m.Version = "2.0." + strconv.Itoa(i)
		m.Arch = "arm"
		b, _ := m.Bytes()
		_, e = tx.Exec("INSERT INTO releases(project_id,version,sequence,security,channel,platform,arch,hardware,sha256,size,payload,signature,published,created) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,1,?)", f.pid, m.Version, m.Sequence, m.Security, m.Channel, m.Platform, m.Arch, m.Hardware, m.SHA256, m.Size, base64.StdEncoding.EncodeToString(b), hex.EncodeToString(ed25519.Sign(f.key, b)), now())
		if e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	w := f.call("GET", "/api/v1/update/demo/latest?sequence=0&platform=test&arch=x64&hardware=demo", nil, false, f.token)
	var latest struct {
		Update  bool    `json:"update"`
		Release release `json:"release"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &latest)
	if w.Code != 200 || !latest.Update || latest.Release.Sequence != 2 {
		t.Fatal("compatible release lost behind other-platform history", w.Body.String())
	}
	before := ""
	seen := map[int64]bool{}
	for page := 0; page < 100; page++ {
		path := "/api/v1/update/demo/releases?limit=7"
		if before != "" {
			path += "&before=" + before
		}
		w = f.call("GET", path, nil, false, f.token)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var entries []release
		if json.Unmarshal(w.Body.Bytes(), &entries) != nil {
			t.Fatal("invalid page")
		}
		for _, v := range entries {
			if seen[v.ID] {
				t.Fatal("duplicate pagination record")
			}
			seen[v.ID] = true
		}
		before = w.Header().Get("X-Next-Before")
		if before == "" {
			break
		}
	}
	if len(seen) != 206 {
		t.Fatal("incomplete history", len(seen))
	}
}
func TestIPv6HTTP(t *testing.T) {
	listener, e := net.Listen("tcp6", "[::1]:0")
	if e != nil {
		t.Skip("IPv6 loopback unavailable on this host")
	}
	f := setup(t)
	server := httptest.NewUnstartedServer(f.a.Handler(nil))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	r, e := client.Get(server.URL + "/healthz")
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || len(data) == 0 {
		t.Fatal("IPv6 health failed")
	}
}
