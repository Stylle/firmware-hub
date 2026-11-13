// fwtool keeps release-signing keys on the publisher's machine, never on Hub.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Stylle/firmware-hub/internal/protocol"
)

func fatal(e error) {
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func main() {
	if len(os.Args) < 2 {
		fatal(fmt.Errorf("usage: fwtool keygen | sign"))
	}
	switch os.Args[1] {
	case "keygen":
		f := flag.NewFlagSet("keygen", flag.ExitOnError)
		path := f.String("out", "publisher.key", "private key path (must not exist)")
		fatal(f.Parse(os.Args[2:]))
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		fatal(e)
		h, e := os.OpenFile(*path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		fatal(e)
		_, e = h.Write([]byte(hex.EncodeToString(key)))
		fatal(e)
		fatal(h.Close())
		fmt.Println("Public key:", hex.EncodeToString(pub))
		fmt.Println("Keep the private key offline; do not upload it to Hub or Git.")
	case "sign":
		f := flag.NewFlagSet("sign", flag.ExitOnError)
		keyPath := f.String("key", "publisher.key", "local private key")
		file := f.String("file", "", "artifact file")
		out := f.String("out", "manifest.json", "signed envelope output")
		var m protocol.Manifest
		f.StringVar(&m.Project, "project", "", "project slug")
		f.StringVar(&m.Version, "version", "", "display version")
		f.Int64Var(&m.Sequence, "sequence", 0, "strictly increasing release sequence")
		f.Int64Var(&m.Security, "security", 0, "minimum security counter")
		f.StringVar(&m.Channel, "channel", "stable", "channel")
		f.StringVar(&m.Platform, "platform", "any", "OS / platform")
		f.StringVar(&m.Arch, "arch", "any", "architecture")
		f.StringVar(&m.Hardware, "hardware", "any", "hardware model")
		f.Int64Var(&m.MinSequence, "min-sequence", 0, "minimum installed sequence")
		days := f.Int("days", 90, "manifest lifetime in days")
		fatal(f.Parse(os.Args[2:]))
		if *days < 1 || *days > 3650 {
			fatal(fmt.Errorf("days must be 1–3650"))
		}
		k, e := os.ReadFile(*keyPath)
		fatal(e)
		key, e := hex.DecodeString(string(k))
		fatal(e)
		if len(key) != 64 {
			fatal(fmt.Errorf("expected 64-byte Ed25519 private key"))
		}
		h, e := os.Open(*file)
		fatal(e)
		hash := sha256.New()
		m.Size, e = io.Copy(hash, h)
		fatal(e)
		fatal(h.Close())
		m.SHA256 = hex.EncodeToString(hash.Sum(nil))
		m.Expires = time.Now().Add(time.Duration(*days) * 24 * time.Hour).Unix()
		payload, e := m.Bytes()
		fatal(e)
		env := protocol.Envelope{Payload: base64.StdEncoding.EncodeToString(payload), Signature: hex.EncodeToString(ed25519.Sign(key, payload))}
		b, e := json.MarshalIndent(env, "", "  ")
		fatal(e)
		output, e := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		fatal(e)
		_, e = output.Write(b)
		fatal(e)
		fatal(output.Close())
		fmt.Printf("Signed %s %s (%d bytes). Upload %s and the artifact.\n", m.Project, m.Version, m.Size, *out)
	default:
		fatal(fmt.Errorf("unknown operation"))
	}
}
