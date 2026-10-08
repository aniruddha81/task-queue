// Command devtoken prints a JWT signed with the local development key, for curl.
//
//	go run ./cmd/devtoken            # a token for a fixed demo user
//	go run ./cmd/devtoken -sub <uuid>
//	go run ./cmd/devtoken -pubkey    # the matching JWT_PUBLIC_KEY value
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"time"
	"uuid"

	"github.com/aniruddha81/task-queue/internal/authn"
)

func main() {
	sub := flag.String("sub", "00000000-0000-7000-8000-000000000001", "owner UUID")
	ttl := flag.Duration("ttl", 24*time.Hour, "token lifetime")
	pub := flag.Bool("pubkey", false, "print the public key instead of a token")
	flag.Parse()

	key := authn.DevKey()
	if *pub {
		fmt.Println(base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
		return
	}
	owner, err := uuid.Parse(*sub)
	if err != nil {
		log.Fatal(err)
	}
	tok, err := authn.Sign(key, owner, *ttl)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(tok)
}
