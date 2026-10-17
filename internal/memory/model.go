// Package memory guarda e sincroniza memórias (notas, decisões e progresso de tarefas).
// Regras de negócio e protocolo: docs/API.md.
package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const DecisionMax = 280

var (
	kinds      = []string{"note", "decision", "progress", "idea", "howto"}
	ideaStates = []string{"open", "converted", "discarded"}
)

type Author struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Memory struct {
	ID           string          `json:"id"`
	Rev          int64           `json:"rev"`
	Kind         string          `json:"kind"`  // note | decision | progress | idea | howto
	Scope        string          `json:"scope"` // global | project | folder
	Workspace    *string         `json:"workspace"`
	Folder       *string         `json:"folder"` // remote git normalizado
	Text         string          `json:"text"`
	Tags         []string        `json:"tags"`
	TaskKey      *string         `json:"taskKey"`
	SupersededBy *string         `json:"supersededBy"`
	Progress     json.RawMessage `json:"progress"`
	Refs         []Ref           `json:"refs"`   // vínculos para outras memórias (ver refs.go)
	Review       json.RawMessage `json:"review"` // marca "a revisar": { reason, source, by, at }
	Status       *string         `json:"status"` // ideia: open | converted | discarded
	Author       *Author         `json:"author"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	Deleted      bool            `json:"deleted"`
}

// ValidationError é um erro de entrada do cliente (vira HTTP 400 / status "error" no sync).
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

func invalid(format string, a ...any) error { return ValidationError{fmt.Sprintf(format, a...)} }

func IsValidation(err error) bool {
	var v ValidationError
	return errors.As(err, &v)
}

func emptyToNil(p *string) *string {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

// Normalize completa padrões e valida as regras de docs/API.md.
func (m *Memory) Normalize() error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	} else if _, err := uuid.Parse(m.ID); err != nil {
		return invalid("id deve ser um uuid: %q", m.ID)
	}
	m.Text = strings.TrimSpace(m.Text)
	if m.Text == "" {
		return invalid("A memória precisa de um texto.")
	}
	if m.Kind == "" {
		m.Kind = "note"
	}
	if !slices.Contains(kinds, m.Kind) {
		return invalid("Tipo inválido: %q (use %s).", m.Kind, strings.Join(kinds, ", "))
	}
	if err := m.normalizeStatus(); err != nil {
		return err
	}
	if m.Kind == "progress" {
		m.Scope = "project" // progresso sempre pertence a um workspace
	}
	if m.Scope == "" {
		m.Scope = "project"
	}
	if !slices.Contains([]string{"global", "project", "folder"}, m.Scope) {
		return invalid("Escopo inválido: %q (use global, project ou folder).", m.Scope)
	}
	m.Workspace, m.Folder, m.TaskKey, m.SupersededBy = emptyToNil(m.Workspace), emptyToNil(m.Folder), emptyToNil(m.TaskKey), emptyToNil(m.SupersededBy)
	if m.Folder != nil {
		f := NormalizeRemote(*m.Folder)
		m.Folder = &f
	}
	switch {
	case m.Scope == "global":
		m.Workspace = nil
	case m.Scope == "project" && m.Workspace == nil:
		return invalid("Memória de projeto precisa de workspace.")
	case m.Scope == "folder" && m.Folder == nil:
		return invalid("Memória de pasta precisa de folder (remote git do repositório).")
	}
	if m.Kind == "decision" {
		if strings.Contains(m.Text, "\n") {
			return invalid("Uma decisão deve caber numa frase (sem quebras de linha).")
		}
		if n := len([]rune(m.Text)); n > DecisionMax {
			return invalid("Uma decisão deve caber numa frase (máx. %d caracteres; tem %d).", DecisionMax, n)
		}
	}
	if m.Kind == "progress" && m.TaskKey == nil {
		return invalid("Memória de progresso precisa de taskKey.")
	}
	m.Tags = normalizeTags(m.Tags)
	refs, err := normalizeRefs(m.ID, m.Refs)
	if err != nil {
		return err
	}
	m.Refs = refs
	if emptyJSON(m.Review) {
		m.Review = nil
	}
	return nil
}

func normalizeTags(tags []string) []string {
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

func strEq(a, b *string) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

// SameContent diz se duas versões têm o mesmo conteúdo (para não acusar conflito à toa).
func SameContent(a, b *Memory) bool {
	return a.Kind == b.Kind && a.Scope == b.Scope && a.Text == b.Text && a.Deleted == b.Deleted &&
		strEq(a.Workspace, b.Workspace) && strEq(a.Folder, b.Folder) && strEq(a.TaskKey, b.TaskKey) &&
		strEq(a.SupersededBy, b.SupersededBy) && slices.Equal(a.Tags, b.Tags) && jsonEq(a.Progress, b.Progress) &&
		slices.Equal(a.Refs, b.Refs) && jsonEq(a.Review, b.Review) && strEq(a.Status, b.Status)
}

func jsonEq(a, b json.RawMessage) bool {
	if len(a) == 0 || string(a) == "null" {
		return len(b) == 0 || string(b) == "null"
	}
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && fmt.Sprint(x) == fmt.Sprint(y)
}

// normalizeStatus: ideia nasce "open" e só aceita os estados conhecidos; os outros tipos não têm estado.
func (m *Memory) normalizeStatus() error {
	m.Status = emptyToNil(m.Status)
	if m.Kind != "idea" {
		m.Status = nil
		return nil
	}
	if m.Status == nil {
		open := "open"
		m.Status = &open
	}
	if !slices.Contains(ideaStates, *m.Status) {
		return invalid("Estado de ideia inválido: %q (use %s).", *m.Status, strings.Join(ideaStates, ", "))
	}
	return nil
}
