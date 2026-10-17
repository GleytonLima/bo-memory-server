package memory

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Tipos de vínculo (docs/API.md → "Vínculos e impacto"). Só "depends" propaga impacto.
const (
	RefDepends     = "depends"     // apoia-se em: se a outra mudar, esta precisa ser revista
	RefRelated     = "related"     // relacionada a
	RefContradicts = "contradicts" // contradiz
)

var refTypes = []string{RefDepends, RefRelated, RefContradicts}

const maxRefs = 30

// Ref aponta de uma memória para outra.
type Ref struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// normalizeRefs valida tipos e ids, tira auto-referência e repetidos (o último tipo vale).
func normalizeRefs(self string, refs []Ref) ([]Ref, error) {
	out := []Ref{}
	for _, r := range refs {
		r.ID, r.Type = strings.TrimSpace(r.ID), strings.TrimSpace(r.Type)
		if r.Type == "" {
			r.Type = RefDepends
		}
		if !slices.Contains(refTypes, r.Type) {
			return nil, invalid("Tipo de vínculo inválido: %q (use depends, related ou contradicts).", r.Type)
		}
		if _, err := uuid.Parse(r.ID); err != nil {
			return nil, invalid("Vínculo para id inválido: %q.", r.ID)
		}
		if r.ID == self {
			continue
		}
		out = slices.DeleteFunc(out, func(x Ref) bool { return x.ID == r.ID })
		out = append(out, r)
	}
	if len(out) > maxRefs {
		return nil, invalid("Vínculos demais (máx. %d).", maxRefs)
	}
	return out, nil
}

// emptyJSON trata null e ausência como a mesma coisa.
func emptyJSON(raw json.RawMessage) bool { return len(raw) == 0 || string(raw) == "null" }
