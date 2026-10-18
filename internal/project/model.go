// Package project guarda o que é preciso para abrir um projeto em outra máquina: a definição do
// projeto (do workspace: pastas pelo remote e colunas) e o layout de cada usuário (grupos e
// terminais). Contrato: docs/API.md → "Projeto e layout".
package project

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"bo-memory-server/internal/memory"
)

const (
	maxFolders   = 100
	maxColumns   = 50
	maxGroups    = 50
	maxTerminals = 200
	maxText      = 2000 // nomes, descrições, comandos
)

var ErrNotFound = errors.New("ainda não há documento para este projeto")

type Author struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Folder struct {
	Name   string `json:"name"`
	Remote string `json:"remote"` // remote normalizado (identidade da pasta)
	URL    string `json:"url"`    // para clonar; sem usuário/senha
}

type Column struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Project struct {
	Workspace string    `json:"workspace"`
	Name      string    `json:"name"`
	Folders   []Folder  `json:"folders"`
	Columns   []Column  `json:"columns"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
	UpdatedBy *Author   `json:"updatedBy"`
}

type Terminal struct {
	Name    string  `json:"name"`
	Folder  string  `json:"folder"` // remote da pasta (vazio = a primeira pasta do projeto)
	Subdir  string  `json:"subdir"` // relativa à pasta, sem ".."
	Startup string  `json:"startup"`
	Color   *string `json:"color"`
}

type Group struct {
	Name      string     `json:"name"`
	Terminals []Terminal `json:"terminals"`
}

type Layout struct {
	Workspace string    `json:"workspace"`
	Groups    []Group   `json:"groups"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ValidationError é entrada inválida do cliente (HTTP 400).
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

func invalid(format string, a ...any) error { return ValidationError{fmt.Sprintf(format, a...)} }

func IsValidation(err error) bool {
	var v ValidationError
	return errors.As(err, &v)
}

func clip(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > maxText {
		return "", invalid("Texto longo demais (máx. %d caracteres).", maxText)
	}
	return s, nil
}

// cleanURL aceita só URLs de clone sem credenciais (https://, ssh://, git@host:…); vazio é permitido.
func cleanURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.Contains(raw, "://") {
		return raw, nil // git@host:dono/repo (o usuário "git" não é segredo)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", invalid("URL de clone inválida: %q.", raw)
	}
	if _, hasPass := u.User.Password(); hasPass || (u.User != nil && u.Scheme != "ssh") {
		return "", invalid("A URL de clone não pode levar usuário ou senha.")
	}
	return raw, nil
}

func (p *Project) validate() error {
	var err error
	if p.Name, err = clip(p.Name); err != nil {
		return err
	}
	if p.Name == "" {
		return invalid("Informe o nome do projeto.")
	}
	p.Folders, p.Columns = nonNil(p.Folders), nonNil(p.Columns)
	if len(p.Folders) > maxFolders || len(p.Columns) > maxColumns {
		return invalid("No máximo %d pastas e %d colunas.", maxFolders, maxColumns)
	}
	seen := map[string]bool{}
	for i := range p.Folders {
		f := &p.Folders[i]
		f.Remote = memory.NormalizeRemote(f.Remote)
		if f.Remote == "" {
			return invalid("Pasta sem remote: só pastas com remote git vão para o servidor.")
		}
		if seen[f.Remote] {
			return invalid("Pasta repetida: %s.", f.Remote)
		}
		seen[f.Remote] = true
		if f.Name, err = clip(f.Name); err != nil {
			return err
		}
		if f.URL, err = cleanURL(f.URL); err != nil {
			return err
		}
	}
	for i := range p.Columns {
		c := &p.Columns[i]
		if c.Name, err = clip(c.Name); err != nil || c.Name == "" {
			return invalid("Coluna sem nome ou com texto longo demais.")
		}
		if c.Description, err = clip(c.Description); err != nil {
			return err
		}
	}
	return nil
}

func (l *Layout) validate() error {
	l.Groups = nonNil(l.Groups)
	if len(l.Groups) > maxGroups {
		return invalid("No máximo %d grupos.", maxGroups)
	}
	total := 0
	for gi := range l.Groups {
		g := &l.Groups[gi]
		var err error
		if g.Name, err = clip(g.Name); err != nil {
			return err
		}
		g.Terminals = nonNil(g.Terminals)
		total += len(g.Terminals)
		for ti := range g.Terminals {
			if err := g.Terminals[ti].validate(); err != nil {
				return err
			}
		}
	}
	if total > maxTerminals {
		return invalid("No máximo %d terminais.", maxTerminals)
	}
	return nil
}

func (t *Terminal) validate() error {
	var err error
	if t.Name, err = clip(t.Name); err != nil {
		return err
	}
	if t.Startup, err = clip(t.Startup); err != nil {
		return err
	}
	t.Folder = memory.NormalizeRemote(t.Folder)
	t.Subdir = strings.Trim(strings.ReplaceAll(strings.TrimSpace(t.Subdir), `\`, "/"), "/")
	for _, part := range strings.Split(t.Subdir, "/") {
		if part == ".." {
			return invalid("A subpasta do terminal não pode subir de nível (..).")
		}
	}
	if strings.Contains(t.Subdir, ":") {
		return invalid("A subpasta do terminal deve ser relativa à pasta do projeto.")
	}
	return nil
}
