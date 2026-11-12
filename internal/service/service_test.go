package service

import "testing"

// Ein bekanntes Programm freigeben gibt alle seine Dateien frei.
func TestSetPhoneAppGroup(t *testing.T) {
	t.Setenv("WM_DATA_DIR", t.TempDir()) // Testmodus: Netzwerk wird nicht verändert
	s := New()
	defer s.Close()
	if err := s.SetPhoneApp("Agent.exe", true); err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	if !cfg.PhoneApps["battle.net.exe"] || !cfg.PhoneApps["agent.exe"] {
		t.Fatalf("Battle.net nicht ganz freigegeben: %v", cfg.PhoneApps)
	}
	s.SetPhoneApp("battle.net.exe", false)
	if len(s.Config().PhoneApps) != 0 {
		t.Fatalf("nicht ganz zurückgenommen: %v", s.Config().PhoneApps)
	}
	s.SetPhoneApp("Discord.exe", true)
	if !s.Config().PhoneApps["discord.exe"] {
		t.Fatal("unbekanntes Programm nicht freigegeben")
	}
}
