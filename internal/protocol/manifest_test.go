package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestRoundTripAndTamper(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	m := Manifest{Project: "demo", Version: "1.2.0", Sequence: 3, Security: 1, Channel: "stable", Platform: "any", Arch: "x64", Hardware: "board-a", SHA256: strings.Repeat("a", 64), Size: 100, Expires: 2000000000}
	b, e := m.Bytes()
	if e != nil {
		t.Fatal(e)
	}
	env := Envelope{Payload: base64.StdEncoding.EncodeToString(b), Signature: hex.EncodeToString(ed25519.Sign(key, b))}
	got, e := env.Verify(pub)
	if e != nil || got != m {
		t.Fatalf("round trip: %+v %v", got, e)
	}
	b[5] ^= 1
	env.Payload = base64.StdEncoding.EncodeToString(b)
	if _, e = env.Verify(pub); e == nil {
		t.Fatal("tamper accepted")
	}
}
func TestRejectNoncanonical(t *testing.T) {
	base := "FH1\ndemo\n1.0\n1\n0\nstable\nany\nany\nany\n" + strings.Repeat("0", 64) + "\n10\n0\n2000000000\n"
	for _, bad := range []string{base + "x", strings.Replace(base, "\n1\n", "\n01\n", 1), strings.Replace(base, "demo", "a\nb", 1), strings.Replace(base, "\n10\n", "\n-1\n", 1), strings.Replace(base, "\n1\n", "\n9223372036854775808\n", 1), strings.Repeat("a", 1025)} {
		if _, e := Parse([]byte(bad)); e == nil {
			t.Fatalf("accepted invalid input %q", bad)
		}
	}
}
func FuzzParse(f *testing.F) {
	f.Add([]byte("FH1\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, e := Parse(b)
		if e == nil {
			canonical, e := m.Bytes()
			if e != nil || string(canonical) != string(b) {
				t.Fatal("canonical mismatch")
			}
		}
	})
}
