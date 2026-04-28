package main

import "testing"

func TestProxyJumpAlias(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"none", ""},
		{"bastion", "bastion"},
		{"  bastion  ", "bastion"},
		{"user@bastion", "bastion"},
		{"user@bastion:2222", "bastion"},
		{"bastion:2222", "bastion"},
		{"user@bastion,user@hop2", "bastion"},
		{"hop1,hop2", "hop1"},
		{"complex-user@bast-1.internal:22", "bast-1.internal"},
	}
	for _, tc := range cases {
		got := proxyJumpAlias(tc.in)
		if got != tc.want {
			t.Errorf("proxyJumpAlias(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
