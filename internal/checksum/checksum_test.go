package checksum

import (
	"strings"
	"testing"
)

func TestKnownDigests(t *testing.T) {
	cases := map[string]string{
		SHA256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		BLAKE3: "ea8f163db38682925e4491c5e58d4bb3506ef8c14eb78a86e908c5624a67200f",
	}
	for algo, want := range cases {
		got, n, err := Reader(algo, strings.NewReader("hello"))
		if err != nil || n != 5 || got != want {
			t.Fatalf("%s: %s %d %v", algo, got, n, err)
		}
	}
	if _, err := New("md5"); err == nil {
		t.Fatal("md5 must be refused")
	}
}
