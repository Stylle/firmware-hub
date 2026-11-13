// testvectors generates ephemeral, cross-language verification data, not keys
// for any deployed project. No private signing material is written.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"os"
	"strings"

	"github.com/Stylle/firmware-hub/internal/protocol"
)

func main() {
	out := flag.String("out", "build/vector.bin", "test vector output")
	flag.Parse()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		panic(e)
	}
	m := protocol.Manifest{Project: "demo", Version: "2.0.0", Sequence: 2, Security: 1, Channel: "stable", Platform: "test", Arch: "x64", Hardware: "demo", SHA256: strings.Repeat("ab", 32), Size: 100, Expires: 2000000000}
	b, e := m.Bytes()
	if e != nil {
		panic(e)
	}
	data := append(append(append([]byte{}, pub...), ed25519.Sign(key, b)...), b...)
	if e = os.WriteFile(*out, data, 0600); e != nil {
		panic(e)
	}
}
