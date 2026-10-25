package nativehost

import (
	"context"
	"testing"
	"time"
)

func TestPipeRoundTrip(t *testing.T) {
	PipeName = `\\.\pipe\WlanMerge-Test-` + t.Name() // nicht mit einem laufenden WlanMerge sprechen
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Offer, 1)
	go Serve(ctx, func(o Offer) Reply {
		got <- o
		return Reply{Accept: true}
	})
	time.Sleep(100 * time.Millisecond)

	r := Forward(Offer{Type: "offer", URL: "https://example.com/big.iso", Cookies: "a=1"})
	if !r.Accept {
		t.Fatalf("Angebot sollte angenommen werden: %+v", r)
	}
	if o := <-got; o.URL != "https://example.com/big.iso" || o.Cookies != "a=1" {
		t.Fatalf("falsch übertragen: %+v", o)
	}
	// Ungültige Nachrichten werden abgelehnt, ohne die App zu fragen.
	for _, bad := range []Offer{
		{Type: "offer", URL: "file:///C:/Windows/win.ini"},
		{Type: "exec", URL: "https://example.com"},
		{Type: "offer", URL: "https://example.com", UserAgent: "x\r\nInjected: 1"},
		{Type: "drop", Token: "../../etc"},
	} {
		if r := Forward(bad); r.Accept {
			t.Fatalf("ungültige Nachricht angenommen: %+v", bad)
		}
	}
	select {
	case o := <-got:
		t.Fatalf("ungültige Nachricht kam bei der App an: %+v", o)
	default:
	}
}
