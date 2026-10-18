package memory

import (
	"encoding/json"
	"testing"
)

// Autoridade: decisão sobe, "a revisar"/substituída/ideia fechada têm teto, sempre em [0,55; 1,5];
// e a relevância ainda manda — uma nota muito mais relevante fica acima de uma decisão.
func TestAuthority(t *testing.T) {
	sup, closed := "x", "converted"
	cases := []struct {
		m    Memory
		want float64
	}{
		{Memory{Kind: "decision"}, 1.15},
		{Memory{Kind: "howto"}, 1.12},
		{Memory{Kind: "note"}, 1},
		{Memory{Kind: "progress"}, 0.9},
		{Memory{Kind: "decision", Review: json.RawMessage(`{"reason":"r"}`)}, 0.8},
		{Memory{Kind: "decision", Review: json.RawMessage(`null`)}, 1.15},
		{Memory{Kind: "decision", SupersededBy: &sup}, 0.65},
		{Memory{Kind: "idea", Status: &closed}, 0.8},
	}
	for _, c := range cases {
		if got := authority(&c.m); got < 0.55 || got > 1.5 || got-c.want > 1e-9 || c.want-got > 1e-9 {
			t.Errorf("%+v: %.2f, esperado %.2f", c.m, got, c.want)
		}
	}
	items := []Memory{{ID: "n", Kind: "note"}, {ID: "d", Kind: "decision"}, {ID: "d2", Kind: "decision"}}
	score := map[string]float64{"n": 1, "d": 0.95, "d2": 0.5}
	got := rerank(items, func(m *Memory) float64 { return score[m.ID] }, 2)
	if len(got) != 2 || got[0].ID != "d" || got[1].ID != "n" {
		t.Fatalf("empate quase: a decisão passa; a muito menos relevante não entra: %+v", got)
	}
	if authorityWindow(5) != 20 || authorityWindow(100) != 400 || authorityWindow(1000) != 1300 {
		t.Fatal("janela de candidatos: 4×, no mínimo 20, no máximo +300")
	}
}
