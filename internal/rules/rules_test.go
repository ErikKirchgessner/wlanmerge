package rules

import (
	"net/netip"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"wlanmerge/internal/config"
)

func TestMatch(t *testing.T) {
	m := Compile([]config.Rule{
		{Match: "*.battle.net", Link: "haupt"},
		{Match: "example.org", Link: "WLAN"},
		{Match: "10.0.0.0/8", Link: "Handy"},
	})
	ip := netip.MustParseAddr("1.2.3.4")
	cases := []struct {
		ip    netip.Addr
		names []string
		want  string
	}{
		{ip, []string{"eu.actual.battle.net"}, "haupt"},
		{ip, []string{"battle.net"}, ""}, // *.x passt nur auf Subdomains
		{ip, []string{"example.org"}, "WLAN"},
		{ip, []string{"cdn.example.org"}, "WLAN"},
		{ip, []string{"notexample.org"}, ""},
		{netip.MustParseAddr("10.1.2.3"), nil, "Handy"},
		{ip, []string{"level3.blizzard.com"}, ""},
	}
	for _, c := range cases {
		got, _ := m.Match(c.ip, c.names)
		if got != c.want {
			t.Errorf("%v %v: %q, erwartet %q", c.ip, c.names, got, c.want)
		}
	}
}

func TestLearnFollowsCNAME(t *testing.T) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("eu.battle.net."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	b.StartAnswers()
	b.CNAMEResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("eu.battle.net."), Class: dnsmessage.ClassINET, TTL: 60},
		dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("edge.example.net.")})
	b.AResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("edge.example.net."), Class: dnsmessage.ClassINET, TTL: 60},
		dnsmessage.AResource{A: [4]byte{5, 6, 7, 8}})
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	c := NewDNSCache()
	c.Learn(msg)
	names := c.Names(netip.MustParseAddr("5.6.7.8"))
	if len(names) != 1 || names[0] != "eu.battle.net" {
		t.Fatalf("Namen falsch: %v", names)
	}
	m := Compile([]config.Rule{{Match: "*.battle.net", Link: "haupt"}})
	if l, ok := m.Match(netip.MustParseAddr("5.6.7.8"), names); !ok || l != "haupt" {
		t.Fatal("Regel sollte über DNS-Namen greifen")
	}
}
