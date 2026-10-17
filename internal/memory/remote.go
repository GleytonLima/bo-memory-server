package memory

import (
	"regexp"
	"strings"
)

var (
	schemeRe   = regexp.MustCompile(`^[a-z+]+://`)
	scpStyleRe = regexp.MustCompile(`^([^@/]+@)?([^:/]+):(.+)$`) // git@host:dono/repo
)

// NormalizeRemote reduz qualquer forma de URL de remote git a "host/dono/repo":
// sem protocolo, sem usuário, sem ".git", ":" vira "/", minúsculas. Ver docs/API.md.
//
//	git@github.com:Gleyton/Repo.git      → github.com/gleyton/repo
//	https://github.com/Gleyton/Repo.git  → github.com/gleyton/repo
//	ssh://git@github.com/gleyton/repo    → github.com/gleyton/repo
func NormalizeRemote(remote string) string {
	r := strings.TrimSpace(strings.ToLower(remote))
	if r == "" {
		return ""
	}
	if schemeRe.MatchString(r) {
		r = schemeRe.ReplaceAllString(r, "")
		if at := strings.Index(r, "@"); at >= 0 && at < strings.Index(r+"/", "/") {
			r = r[at+1:] // usuário/senha antes do host
		}
	} else if m := scpStyleRe.FindStringSubmatch(r); m != nil {
		r = m[2] + "/" + m[3]
	}
	r = strings.TrimSuffix(strings.TrimRight(r, "/"), ".git")
	if host, rest, ok := strings.Cut(r, "/"); ok {
		if h, _, hasPort := strings.Cut(host, ":"); hasPort {
			host = h // ignora a porta (ssh://host:22/…)
		}
		r = host + "/" + rest
	}
	return r
}
