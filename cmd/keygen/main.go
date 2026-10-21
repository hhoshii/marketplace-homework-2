// keygen writes signing configuration without printing private material.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
)

func main() {
	output := flag.String("out", ".env.jwt", "new environment file (will not overwrite)")
	flag.Parse()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		panic(err)
	}
	_, err = fmt.Fprintf(file, "JWT_PRIVATE_KEY=%s\nJWT_PUBLIC_KEY=%s\n", hex.EncodeToString(key.Seed()), hex.EncodeToString(public))
	if err != nil {
		panic(err)
	}
	if err = file.Close(); err != nil {
		panic(err)
	}
	fmt.Println("JWT keys written to", *output)
}
