package ssh

import "testing"

func TestParseFailedLine(t *testing.T) {
	line := "2026-09-13T15:51:33.563415+03:30 vm12870-12276 sshd[2886060]: Failed password for root from 62.60.130.242 port 2586 ssh2"
	f, ok := parseFailedLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if f.User != "root" || f.IP != "62.60.130.242" || f.Detail != "failed password" {
		t.Fatalf("unexpected parse: %+v", f)
	}
}

func TestFormatFailedLoginsEmpty(t *testing.T) {
	if FormatFailedLogins(nil) == "" {
		t.Fatal("expected placeholder")
	}
}
