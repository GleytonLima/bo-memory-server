package memory

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Palavras de ligação (PT e EN) que não entram na busca por texto: com prefixo e OR, "de" vira de:*
// e casa com "decisão", "deploy"… e só faz barulho no ranking (ideia do ai-memory: medido, +5
// pontos de hit@5). Comparação sem acento e sem maiúsculas. Mesma lista em
// organizer/src/main/memory/stopwords.js — mude as duas juntas.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a o as os um uma uns umas de da do das dos em na no nas nos ao aos por pelo pela pelos pelas
		para pra com sem e ou que qual quais como onde quando quanto quanta se e ser foi era sao esta isso isto esse essa
		este esta eu voce nos gente mais mas ja seu sua meu minha the of to in on for and or is are an with it be at by
		this that how what why when where which`) {
		stopwords[w] = true
	}
}

// fold: sem acentos e em minúsculas ("É" → "e").
func fold(w string) string {
	out, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn))), w)
	if err != nil {
		out = w
	}
	return strings.ToLower(out)
}

// contentTerms: os termos que contam — sem stopwords e sem letras soltas; se nada sobrar, os originais.
func contentTerms(terms []string) []string {
	kept := []string{}
	for _, t := range terms {
		if utf8.RuneCountInString(t) > 1 && !stopwords[fold(t)] {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return terms
	}
	return kept
}
