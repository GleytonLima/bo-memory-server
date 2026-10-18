package memory

import "sort"

// Autoridade de uma memória no ranking (ideia do ai-memory): depois da relevância, um fator limitado
// a [0,55; 1,5] — decisão e "como fazer" sobem um pouco; progresso e ideia descem; "a revisar",
// substituída e ideia fechada têm teto. A relevância continua mandando: nada sai do resultado.
// Mesmos pesos em organizer/src/main/memory/authority.js — mude os dois juntos.
var kindAuthority = map[string]float64{"decision": 0.15, "howto": 0.12, "note": 0, "progress": -0.1, "idea": -0.05}

func authority(m *Memory) float64 {
	f := 1 + kindAuthority[m.Kind]
	if m.SupersededBy != nil {
		f = min(f, 0.65)
	} else if len(m.Review) > 0 && string(m.Review) != "null" {
		f = min(f, 0.8)
	}
	if m.Status != nil && *m.Status != "open" { // ideia fechada, progresso de tarefa concluída
		f = min(f, 0.8)
	}
	return max(0.55, min(1.5, f))
}

// authorityWindow: candidatos antes da autoridade — folga para uma decisão um pouco abaixo entrar.
func authorityWindow(limit int) int {
	return min(max(limit*4, 20), limit+300)
}

// rerank ordena por score × autoridade (maior = melhor) e corta em limit.
func rerank(items []Memory, score func(*Memory) float64, limit int) []Memory {
	s := make(map[string]float64, len(items))
	for i := range items {
		s[items[i].ID] = score(&items[i]) * authority(&items[i])
	}
	sort.SliceStable(items, func(a, b int) bool { return s[items[a].ID] > s[items[b].ID] })
	return head(items, limit)
}
