// Package task guarda e sincroniza o quadro de tarefas de cada workspace (o quadro é do time).
// Protocolo: docs/API.md → "Quadro de tarefas".
package task

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxDoc  = 256 << 10 // 256 KiB de conteúdo por tarefa
	maxID   = 64
	maxPage = 500
)

var keyRe = regexp.MustCompile(`^(.{1,30})-(\d{1,9})$`)

type Author struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Task struct {
	Workspace string          `json:"workspace"`
	ID        string          `json:"id"`
	Key       string          `json:"key"`
	Rev       int64           `json:"rev"`
	Deleted   bool            `json:"deleted"`
	Doc       json.RawMessage `json:"task"`
	Owner     *Author         `json:"owner"` // quem está com a tarefa (só o claim muda)
	UpdatedBy *Author         `json:"updatedBy"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

var ErrNotFound = errors.New("tarefa não encontrada")

type Change struct {
	Op        string          `json:"op"` // upsert | delete
	Workspace string          `json:"workspace"`
	BaseRev   int64           `json:"baseRev"`
	ID        string          `json:"id"`
	Key       string          `json:"key"`
	Doc       json.RawMessage `json:"task"`
}

type Result struct {
	ID     string `json:"id"`
	Status string `json:"status"` // ok | conflict | error
	Rev    int64  `json:"rev,omitempty"`
	Key    string `json:"key,omitempty"`
	Server *Task  `json:"server,omitempty"`
	Error  string `json:"error,omitempty"`
}

type PullResponse struct {
	Items []Task `json:"items"`
	Rev   int64  `json:"rev"`
	More  bool   `json:"more"`
}

func errorResult(id, msg string) Result { return Result{ID: id, Status: "error", Error: msg} }

// validate devolve a mensagem de erro da mudança ("" = válida) e normaliza workspace e chave.
func (c *Change) validate() string {
	c.Workspace = strings.ToLower(strings.TrimSpace(c.Workspace))
	c.Key = strings.TrimSpace(c.Key)
	switch {
	case c.Workspace == "":
		return "Informe o workspace da tarefa."
	case c.ID == "" || len(c.ID) > maxID:
		return "Id da tarefa inválido."
	case c.Op == "delete":
		return ""
	case c.Op != "upsert":
		return "Operação inválida: use upsert ou delete."
	case !keyRe.MatchString(c.Key):
		return "Chave inválida (ex.: BO-27)."
	case len(c.Doc) > maxDoc:
		return "Tarefa grande demais (máx. 256 KiB)."
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(c.Doc, &probe) != nil {
		return "O conteúdo da tarefa deve ser um objeto JSON."
	}
	return ""
}

// nextKey: a próxima chave livre do mesmo prefixo, dado o maior número já usado.
func nextKey(key string, max int) string {
	m := keyRe.FindStringSubmatch(key)
	return m[1] + "-" + strconv.Itoa(max+1)
}

// seqOf: o número da chave dentro do prefixo (0 se não casar).
func seqOf(key, prefix string) int {
	m := keyRe.FindStringSubmatch(key)
	if m == nil || m[1] != prefix {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

func prefixOf(key string) string { return keyRe.FindStringSubmatch(key)[1] }
