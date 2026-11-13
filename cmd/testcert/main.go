// testcert creates an ephemeral CA/server pair for localhost HTTPS tests only.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	out := flag.String("out", ".local/tls", "ephemeral test directory")
	flag.Parse()
	must(os.MkdirAll(*out, 0700))
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(e)
	start := time.Now().Add(-time.Hour)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Firmware Hub Local Test CA"}, NotBefore: start, NotAfter: start.Add(7 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	must(e)
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(e)
	server := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, NotBefore: start, NotAfter: start.Add(6 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, server, ca, &key.PublicKey, caKey)
	must(e)
	priv, e := x509.MarshalECPrivateKey(key)
	must(e)
	must(os.WriteFile(filepath.Join(*out, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600))
	must(os.WriteFile(filepath.Join(*out, "server.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
	must(os.WriteFile(filepath.Join(*out, "server.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: priv}), 0600))
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
