package api

import "testing"

func TestMentionText(t *testing.T) {
	ms := []Mention{{JID: "61400000001@s.whatsapp.net", Name: "Alice"}, {JID: "900@lid", Name: "You"}, {JID: "61400000002@s.whatsapp.net"}}
	for in, want := range map[string]string{
		"@61400000001 hi":                   "@Alice hi",
		"hi @61400000001":                   "hi @Alice",
		"@614000000012 @61400000001, @900!": "@614000000012 @Alice, @You!",
		"@61400000001@61400000001":          "@Alice@Alice",
		"@61400000002 unnamed":              "@61400000002 unnamed",
		"no one":                            "no one",
	} {
		if got := MentionText(in, ms); got != want {
			t.Errorf("MentionText(%q) = %q, want %q", in, got, want)
		}
	}
}
