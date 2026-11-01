package main

import (
	"regexp"
	"testing"
)

var w3c = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

func TestNewTraceparentIsW3C(t *testing.T) {
	a, b := newTraceparent(), newTraceparent()
	if !w3c.MatchString(a) {
		t.Errorf("newTraceparent() = %q, not a version-00 sampled traceparent", a)
	}
	if a == b {
		t.Errorf("two calls returned the same traceparent %q", a)
	}
}

func TestTraceIDOf(t *testing.T) {
	cases := map[string]string{
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01": "4bf92f3577b34da6a3ce929d0e0e4736", // W3C spec example
		"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01": "",                                 // unknown version
		"00-4bf92f3577b34da6-00f067aa0ba902b7-01":                 "",                                 // short trace id
		"": "",
	}
	for in, want := range cases {
		if got := traceIDOf(in); got != want {
			t.Errorf("traceIDOf(%q) = %q, want %q", in, got, want)
		}
	}
}
