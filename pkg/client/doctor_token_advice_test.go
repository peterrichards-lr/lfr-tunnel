package client

import (
	"strings"
	"testing"
)

// #2593: doctor told a user whose refused token came from LFT_CLIENT_TOKEN to run
// `lfr-tunnel login`. Login saves ~/.lfr-tunnel/token, which the variable outranks, so following
// the advice changed nothing and doctor failed the same way.
func TestDoctorRefusedTokenAdviceFollowsItsSource(t *testing.T) {
	old := verifyLoginToken
	t.Cleanup(func() { verifyLoginToken = old })
	verifyLoginToken = func(string, string) (loginTokenCheck, string) { return loginTokenRefused, "" }

	cases := []struct {
		source     string
		offerLogin bool // whether "run `lfr-tunnel login`" alone is offered as a fix
	}{
		{"LFT_CLIENT_TOKEN environment variable", false},
		{"LFT_TOKEN environment variable", false},
		{"config file", false},
		{"LFT_TOKEN_FILE (/tmp/tok)", false},
		{"token file (/home/u/.lfr-tunnel/token)", true},
		{"LDM credentials file (/home/u/.ldm/credentials)", true},
	}
	for _, c := range cases {
		got := doctorTokenCheck(DoctorInput{ServerURL: "https://gw.example", Token: "abc", TokenSource: c.source})
		if got.Status != DoctorFail {
			t.Fatalf("%s: status %s, want FAIL", c.source, got.Status)
		}
		detail := strings.Join(got.Detail, " ")
		offers := strings.Contains(detail, "or run `lfr-tunnel login`.")
		if offers != c.offerLogin {
			t.Errorf("%s: offers login as a fix = %v, want %v:\n  %s", c.source, offers, c.offerLogin, detail)
		}
		if !c.offerLogin && !strings.Contains(detail, "put it in "+c.source) {
			t.Errorf("%s: the advice does not say to replace the token where it was read from:\n  %s", c.source, detail)
		}
	}
}
