package config

import (
	"testing"
	"time"
)

func TestPhoneDefaultLimit(t *testing.T) {
	c := Default()
	if c.LimitFor("Ethernet 2", true) != DefaultPhoneLimit {
		t.Fatal("Handy sollte 2 GB Standardlimit haben")
	}
	if c.LimitFor("WLAN", false) != 0 {
		t.Fatal("WLAN sollte kein Limit haben")
	}
	c.Limits["Ethernet 2"] = 0
	if c.LimitFor("Ethernet 2", true) != 0 {
		t.Fatal("ausdrücklich abgeschaltetes Limit muss gelten")
	}
}

func TestLastReset(t *testing.T) {
	c := Default()
	c.ResetDay = 15
	loc := time.Local
	cases := []struct{ now, want time.Time }{
		{time.Date(2026, 10, 20, 12, 0, 0, 0, loc), time.Date(2026, 10, 15, 0, 0, 0, 0, loc)},
		{time.Date(2026, 10, 14, 23, 0, 0, 0, loc), time.Date(2026, 9, 15, 0, 0, 0, 0, loc)},
		{time.Date(2026, 1, 3, 9, 0, 0, 0, loc), time.Date(2025, 12, 15, 0, 0, 0, 0, loc)},
	}
	for _, x := range cases {
		if got := c.LastReset(x.now); !got.Equal(x.want) {
			t.Errorf("%v: %v, erwartet %v", x.now, got, x.want)
		}
	}
	c.ResetDay = 0
	if !c.LastReset(time.Now()).IsZero() {
		t.Fatal("ResetDay 0 = nie zurücksetzen")
	}
}
