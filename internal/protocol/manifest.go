// Package protocol defines FH1, the deliberately small signed release format.
// FH1 is not TUF or SUIT. It authenticates a fixed, bounded byte representation.
package protocol

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var Identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,63}$`)

type Manifest struct {
	Project     string `json:"project"`
	Version     string `json:"version"`
	Sequence    int64  `json:"sequence"`
	Security    int64  `json:"security"`
	Channel     string `json:"channel"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Hardware    string `json:"hardware"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	MinSequence int64  `json:"min_sequence"`
	Expires     int64  `json:"expires"`
}
type Envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// Bytes returns the only accepted serialization, including the final newline.
func (m Manifest) Bytes() ([]byte, error) {
	for _, s := range []string{m.Project, m.Version, m.Channel, m.Platform, m.Arch, m.Hardware} {
		if !Identifier.MatchString(s) {
			return nil, errors.New("invalid manifest identifier")
		}
	}
	h, e := hex.DecodeString(m.SHA256)
	if e != nil || len(h) != 32 || strings.ToLower(m.SHA256) != m.SHA256 {
		return nil, errors.New("invalid SHA-256")
	}
	if m.Sequence < 1 || m.Security < 0 || m.Size < 1 || m.MinSequence < 0 || m.Expires < 1 {
		return nil, errors.New("invalid manifest number")
	}
	return []byte(fmt.Sprintf("FH1\n%s\n%s\n%d\n%d\n%s\n%s\n%s\n%s\n%s\n%d\n%d\n%d\n", m.Project, m.Version, m.Sequence, m.Security, m.Channel, m.Platform, m.Arch, m.Hardware, m.SHA256, m.Size, m.MinSequence, m.Expires)), nil
}
func Parse(b []byte) (Manifest, error) {
	var m Manifest
	if len(b) > 1024 {
		return m, errors.New("manifest too large")
	}
	v := strings.Split(string(b), "\n")
	if len(v) != 14 || v[0] != "FH1" || v[13] != "" {
		return m, errors.New("invalid FH1 framing")
	}
	m.Project = v[1]
	m.Version = v[2]
	m.Channel = v[5]
	m.Platform = v[6]
	m.Arch = v[7]
	m.Hardware = v[8]
	m.SHA256 = v[9]
	for _, n := range []struct {
		dst *int64
		s   string
	}{{&m.Sequence, v[3]}, {&m.Security, v[4]}, {&m.Size, v[10]}, {&m.MinSequence, v[11]}, {&m.Expires, v[12]}} {
		x, e := strconv.ParseInt(n.s, 10, 64)
		if e != nil {
			return m, e
		}
		*n.dst = x
	}
	canonical, e := m.Bytes()
	if e != nil {
		return m, e
	}
	if string(canonical) != string(b) {
		return m, errors.New("noncanonical manifest")
	}
	return m, nil
}
func (e Envelope) Verify(key []byte) (Manifest, error) {
	var m Manifest
	b, err := base64.StdEncoding.Strict().DecodeString(e.Payload)
	if err != nil {
		return m, err
	}
	s, err := hex.DecodeString(e.Signature)
	if err != nil || len(s) != 64 || len(key) != 32 {
		return m, errors.New("invalid signature encoding")
	}
	if !ed25519.Verify(key, b, s) {
		return m, errors.New("signature verification failed")
	}
	return Parse(b)
}
