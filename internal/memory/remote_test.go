package memory

import "testing"

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Gleyton/Repo.git":        "github.com/gleyton/repo",
		"https://github.com/Gleyton/Repo.git":    "github.com/gleyton/repo",
		"ssh://git@github.com/gleyton/repo":      "github.com/gleyton/repo",
		"https://user:pw@gitlab.com/a/b/c.git/":  "gitlab.com/a/b/c",
		"ssh://git@host.local:2222/team/svc.git": "host.local/team/svc",
		"github.com/gleyton/repo":                "github.com/gleyton/repo",
		"  ":                                     "",
	}
	for in, want := range cases {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("NormalizeRemote(%q) = %q; quero %q", in, got, want)
		}
	}
}
