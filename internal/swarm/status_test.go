package swarm

import (
	"context"
	"slices"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// A running join set is reported, registration by registration, for as long
// as it runs: the parent relay by the host of its URL, never a credential.
func TestJoinsReportTheRunningRegistrations(t *testing.T) {
	cfg := &config.Config{}
	cfg.Swarm.Join = []config.SwarmJoin{
		{URL: "http://user:secret@127.0.0.1:1/base?token=x", Name: "alpha", PairingToken: "pair"},
		{URL: "https://relay.example:8443", Name: "alpha", PairingToken: "pair", AdvertiseURL: "http://127.0.0.1:2"},
	}
	set, err := StartJoins(context.Background(), cfg, StartJoinsOptions{Kind: KindAgent})
	if err != nil {
		t.Fatal(err)
	}
	got := Joins()
	want := []JoinStatus{
		{Relay: "127.0.0.1:1", Kind: KindAgent, Transport: TransportTunnel},
		{Relay: "relay.example:8443", Kind: KindAgent, Transport: TransportDirect},
	}
	for _, w := range want {
		if !slices.ContainsFunc(got, func(j JoinStatus) bool { return j.Relay == w.Relay && j.Kind == w.Kind && j.Transport == w.Transport }) {
			t.Errorf("joins = %+v, want one like %+v", got, w)
		}
	}
	set.Stop()
	for _, j := range Joins() {
		if j.Relay == "127.0.0.1:1" || j.Relay == "relay.example:8443" {
			t.Errorf("a stopped join set is still reported: %+v", j)
		}
	}
}

// The relay this process runs is readable while it is published, and only
// then.
func TestRelayNodesWhileTheRelayIsPublished(t *testing.T) {
	if _, ok := RelayNodes(); ok {
		t.Fatal("a relay is reported before any was published")
	}
	nodes := []NodeInfo{{Name: "alpha", Kind: KindAgent, Transport: TransportDirect, Online: true}}
	withdraw := PublishRelay(func() []NodeInfo { return nodes })
	got, ok := RelayNodes()
	if !ok || len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("relay nodes = %+v, %v", got, ok)
	}
	withdraw()
	withdraw()
	if _, ok := RelayNodes(); ok {
		t.Fatal("a withdrawn relay is still reported")
	}
}
