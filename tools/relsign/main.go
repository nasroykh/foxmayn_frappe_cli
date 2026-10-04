// Command relsign creates the release signing key and signs checksums.txt.
// It is not shipped; GoReleaser runs it from the release workflow.
//
//	go run ./tools/relsign keygen <private-key-file>   write a new seed (0600), print the public key
//	go run ./tools/relsign sign <file> <signature-file> sign with $FFC_RELEASE_SIGNING_KEY
//	go run ./tools/relsign verify <file> <signature-file> check against the embedded release keys
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/relsig"
)

const keyEnv = "FFC_RELEASE_SIGNING_KEY"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "relsign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: relsign keygen <key-file> | sign <file> <sig-file> | verify <file> <sig-file>")
	}
	switch {
	case args[0] == "keygen" && len(args) == 2:
		return keygen(args[1])
	case args[0] == "sign" && len(args) == 3:
		return sign(args[1], args[2])
	case args[0] == "verify" && len(args) == 3:
		return verify(args[1], args[2])
	}
	return fmt.Errorf("bad arguments: %q", args)
}

func keygen(path string) error {
	pub, seed, err := relsig.GenerateKey()
	if err != nil {
		return err
	}
	// O_EXCL: never overwrite an existing key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(seed + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "private key written to %s — store it as the %s secret and keep an offline copy\n", path, keyEnv)
	fmt.Println(pub)
	return nil
}

func sign(path, sigPath string) error {
	seed := os.Getenv(keyEnv)
	if seed == "" {
		return fmt.Errorf("%s is not set", keyEnv)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig, err := relsig.Sign(seed, data)
	if err != nil {
		return err
	}
	// Refuse to publish a signature that released binaries cannot verify,
	// e.g. a secret that does not match the key embedded in this commit.
	if err := relsig.Verify(relsig.ReleaseKeys, data, sig); err != nil {
		pub, _ := relsig.PublicKey(seed)
		return fmt.Errorf("the %s secret (public key %s) does not match relsig.ReleaseKeys: %w", keyEnv, pub, err)
	}
	return os.WriteFile(sigPath, sig, 0o644)
}

func verify(path, sigPath string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return err
	}
	if err := relsig.Verify(relsig.ReleaseKeys, data, sig); err != nil {
		return err
	}
	fmt.Println("OK")
	return nil
}
