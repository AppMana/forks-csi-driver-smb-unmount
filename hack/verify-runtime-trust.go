package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	blocked := map[string]bool{
		"86a1ecba089c4a8d3bbe2734c612ba341d813e043cf9e8a862cd5c57a36bbe6b": true,
		"d7a7a0fb5d7e2731d771e9484ebcdef71d5f0c3e0a2948782bc83ee0ea699ef4": true,
	}
	count := 0
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			panic(err)
		}
		fingerprint := fmt.Sprintf("%x", sha256.Sum256(block.Bytes))
		if blocked[fingerprint] {
			panic("distrusted root in runtime bundle: " + fingerprint)
		}
		count++
	}
	if count < 100 {
		panic("runtime CA bundle missing or unexpectedly small")
	}
	fmt.Printf("Verified %d runtime certificates\n", count)
}
