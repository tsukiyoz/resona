package ts3

import (
	"context"
	"testing"
)

func TestBoolParam(t *testing.T) {
	if boolParam(true) != "1" || boolParam(false) != "0" {
		t.Fatal("invalid TS3 boolean encoding")
	}
}

func TestResolveNumericEndpoint(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9988", "[::1]:9988"} {
		got, err := resolveEndpoint(context.Background(), address)
		if err != nil || got != address {
			t.Fatalf("resolve %q = %q, %v", address, got, err)
		}
	}
}
