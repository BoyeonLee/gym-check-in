// Command hashpw produces a bcrypt hash for a single password supplied as
// argv[1]. It is intended for seed/reset workflows where a hashed value
// must be substituted into SQL via psql -v.
//
// The tool deliberately never echoes the plaintext password (not even on
// stderr usage hints) so it can be used inside shell pipelines without
// risking a leak into history/logs.
package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost mirrors the production cost used by the auth layer so seed
// hashes and runtime-generated hashes have the same compute envelope.
const bcryptCost = 12

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" {
		fmt.Fprintln(os.Stderr, "usage: hashpw <password>")
		fmt.Fprintln(os.Stderr, "       prints a bcrypt hash to stdout (1 line)")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(os.Args[1]), bcryptCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hashpw: failed to hash password")
		os.Exit(1)
	}

	// Single line of output, no trailing chatter so the value can be
	// captured directly: HASH=$(go run ./cmd/hashpw "$pw").
	fmt.Println(string(hash))
}
