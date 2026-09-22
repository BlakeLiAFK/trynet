package main

import "testing"

func TestEnvOr(t *testing.T) {
	t.Setenv("TRYNET_TEST_STR", "")
	if got := envOr("TRYNET_TEST_STR", "def"); got != "def" {
		t.Errorf("empty env should fall back to default, got %q", got)
	}
	t.Setenv("TRYNET_TEST_STR", "set")
	if got := envOr("TRYNET_TEST_STR", "def"); got != "set" {
		t.Errorf("got %q, want %q", got, "set")
	}
}

func TestEnvOrInt(t *testing.T) {
	t.Setenv("TRYNET_TEST_INT", "")
	if got := envOrInt("TRYNET_TEST_INT", 7); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
	t.Setenv("TRYNET_TEST_INT", "42")
	if got := envOrInt("TRYNET_TEST_INT", 7); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
	t.Setenv("TRYNET_TEST_INT", "not-a-number")
	if got := envOrInt("TRYNET_TEST_INT", 7); got != 7 {
		t.Errorf("invalid env should fall back to default, got %d", got)
	}
}

func TestEnvOrBool(t *testing.T) {
	t.Setenv("TRYNET_TEST_BOOL", "")
	if got := envOrBool("TRYNET_TEST_BOOL", false); got != false {
		t.Errorf("got %v, want false", got)
	}
	t.Setenv("TRYNET_TEST_BOOL", "true")
	if got := envOrBool("TRYNET_TEST_BOOL", false); got != true {
		t.Errorf("got %v, want true", got)
	}
}
