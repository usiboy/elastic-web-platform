package main

import "testing"

func TestGatewayRequiresTLSStateAndHTTPSControlOrigin(t *testing.T) {
	base := config{address: "127.0.0.1:8098", databaseURL: "postgres://localhost/test", artifactDir: "/tmp/ewp-artifacts", controlOrigin: "https://console.preview.test", tlsCert: "cert.pem", tlsKey: "key.pem"}
	if err := validateConfig(base); err != nil {
		t.Fatal(err)
	}
	cases := []config{
		{address: base.address, databaseURL: base.databaseURL, artifactDir: base.artifactDir, controlOrigin: base.controlOrigin},
		{address: base.address, databaseURL: base.databaseURL, artifactDir: base.artifactDir, controlOrigin: "http://console.preview.test", tlsCert: base.tlsCert, tlsKey: base.tlsKey},
		{address: "0.0.0.0:8098", databaseURL: base.databaseURL, artifactDir: "", controlOrigin: base.controlOrigin, tlsCert: base.tlsCert, tlsKey: base.tlsKey},
	}
	for _, input := range cases {
		if err := validateConfig(input); err == nil {
			t.Fatalf("unsafe gateway config accepted: %+v", input)
		}
	}
}
