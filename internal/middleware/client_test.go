package middleware

import "testing"

func TestClientKey(t *testing.T) {
	proxies := newProxySet([]string{"10.0.0.1", "10.0.0.0/8"})
	cases := []struct {
		name   string
		remote string
		xff    string
		want   string
	}{
		{name: "ipv4 ignores forwarded header", remote: "192.0.2.10:1234", xff: "203.0.113.5", want: "192.0.2.10"},
		{name: "ipv6", remote: "[2001:db8::1]:443", want: "2001:db8::1"},
		{name: "trusted peer uses forwarded client", remote: "10.0.0.1:5000", xff: "203.0.113.8", want: "203.0.113.8"},
		{name: "walk skips trusted hops", remote: "10.1.2.3:5000", xff: "198.51.100.9, 10.0.0.1", want: "198.51.100.9"},
		{name: "only trusted hops fall back to peer", remote: "10.0.0.1:5000", xff: "10.0.0.2, 10.8.8.8", want: "10.0.0.1"},
		{name: "bare ipv6", remote: "2001:db8::5", want: "2001:db8::5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clientKey(tc.remote, tc.xff, proxies)
			if got != tc.want {
				t.Fatalf("clientKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"192.0.2.10:1234":   "192.0.2.10",
		"[2001:db8::1]:443": "2001:db8::1",
		"192.0.2.10":        "192.0.2.10",
		"[2001:db8::1]":     "2001:db8::1",
		"  192.0.2.10:9  ":  "192.0.2.10",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Fatalf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
