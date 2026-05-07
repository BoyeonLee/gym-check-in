package main

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestHashpw_OutputIsValidBcrypt builds the binary and runs it, checking that
// the printed hash uses the expected bcrypt prefix and round-trips through
// bcrypt.CompareHashAndPassword.
func TestHashpw_OutputIsValidBcrypt(t *testing.T) {
	const pw = "test1234A"
	out, err := goRun(t, pw)
	if err != nil {
		t.Fatalf("hashpw exited with error: %v\noutput=%q", err, out)
	}
	hash := strings.TrimSpace(out)

	// $2a$12$ or $2b$12$ — golang.org/x/crypto/bcrypt prints "$2a$".
	prefix := regexp.MustCompile(`^\$2[ab]\$12\$`)
	if !prefix.MatchString(hash) {
		t.Fatalf("hash should start with bcrypt cost-12 prefix, got %q", hash)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)); err != nil {
		t.Fatalf("bcrypt.Compare failed: %v", err)
	}
	if strings.Contains(hash, pw) {
		t.Fatalf("hash output must not contain the plaintext password")
	}
}

func TestHashpw_RejectsEmptyArg(t *testing.T) {
	out, err := goRun(t, "")
	if err == nil {
		t.Fatalf("expected non-zero exit for empty password, got output=%q", out)
	}
	// Output (stderr) must not echo any plaintext (we passed empty so this is
	// trivially true; the contract still holds for arbitrary input).
}

func TestHashpw_RejectsNoArgs(t *testing.T) {
	cmd := exec.Command("go", "run", ".")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected non-zero exit when no args supplied; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr should include usage hint, got %q", stderr.String())
	}
}

// goRun executes `go run .` in this command's directory with the given
// argument and returns the combined stdout (the hash, when successful).
func goRun(t *testing.T, arg string) (string, error) {
	t.Helper()
	args := []string{"run", "."}
	if arg != "" {
		args = append(args, arg)
	} else {
		// Pass an explicit empty argument so the program sees argv[1] = "".
		args = append(args, "")
	}
	cmd := exec.Command("go", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), nil
}
