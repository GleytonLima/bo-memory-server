# Busca semântica (opcional)

Com um provedor de embeddings configurado, a busca da web (`GET /v1/memories?q=`) acha também
memórias **parecidas**, sem as mesmas palavras: "banco de dados" encontra "Usamos PostgreSQL…".
Sem provedor, a busca continua só por texto (full-text, sem acentos) — nada muda.

## Como funciona

- **Vetores no PostgreSQL com [pgvector](https://github.com/pgvector/pgvector)**, na tabela
  `memory_embeddings` (uma linha por memória: modelo, hash do texto, vetor). A extensão e a tabela
  são criadas na subida do `serve`, **fora das migrações**: pgvector é opcional, e um banco sem
  ele segue funcionando (o log avisa "busca semântica desligada").
- **Indexação em segundo plano:** logo depois de cada gravação e a cada 30 s, o servidor gera os
  vetores do que falta — memória nova, texto/tags/pasta mudados (hash diferente) ou outro modelo.
  Lotes de 32; o texto enviado é `texto + tags + repo` (até 8000 caracteres), como no app.
  Provedor fora do ar: tenta de novo no próximo ciclo (o erro aparece uma vez no log).
- **Busca híbrida:** os 50 melhores por texto e os 50 mais parecidos (cosseno ≥
  `BO_EMBED_MIN_SIMILARITY`) entre as memórias que a consulta pode ver são fundidos por
  *reciprocal rank fusion* (k = 60), igual ao app desktop. A resposta traz `mode: "hybrid"` e
  marca com `match: "semantic"` o que veio só pelo significado (a web mostra "parecida").
  Se o provedor falhar na hora, volta `mode: "text"` com `warning` — a busca nunca quebra.
- **Autoridade depois da fusão** (vale também para a busca só por texto, sobre o `ts_rank`): um
  fator limitado a [0,55; 1,5] — decisão ×1,15, "como fazer" ×1,12, progresso ×0,9, ideia ×0,95;
  "a revisar" no máximo ×0,8, substituída ×0,65, ideia fechada ×0,8 (`internal/memory/authority.go`,
  mesmos pesos do app). A relevância continua mandando; nada sai do resultado.
- **Stopwords** (PT e EN, sem acento) e letras soltas não entram na consulta por texto
  (`internal/memory/stopwords.go`): com prefixo e OR, "de" virava `de:*` e casava com quase tudo.

**Qualidade em português (medido em 2026-10-18, memória real do projeto, 74 memórias, 8 perguntas
parafraseadas):** com `text-embedding-nomic-embed-text-v1.5` (LM Studio) a híbrida acertou 1/8 no 1º
lugar sem prefixos e 2/8 com eles; só pelo vetor, 0/8, com similaridade 0,6–0,76 para quase tudo.
O nomic é treinado sobretudo em inglês: para conteúdo em português, prefira um modelo multilíngue
(ex.: `multilingual-e5-small/base`, `bge-m3`) e compare com o mesmo conjunto antes de trocar.

## Avaliação

`go test ./internal/memory -run TestSearchEval -v` grava `testdata/search-eval.json` (o mesmo
conjunto do app: `organizer/test/fixtures/search-eval.json`) e mede hit@1, hit@5 e MRR da busca por
texto; o teste falha se ficar abaixo do piso. Mudou o ranking? Rode antes e depois. Em 2026-10-18:
0,64 / 0,92 / 0,753 → com stopwords 0,88 / 1 / 0,94 → com autoridade 1 / 1 / 1 (25 perguntas; o
conjunto saturou — aumente-o com perguntas reais antes de afinar pesos).
- Vetor sem dimensão fixa e busca exata (sem índice ANN): trocar de modelo só reindexa, e para os
  volumes de um time (milhares de memórias) a busca exata é rápida e precisa.

## Configuração

| Variável | Uso |
| --- | --- |
| `BO_EMBED_PROVIDER` | `ollama`, `openai` (qualquer API compatível com `/embeddings`) ou `azure` (Azure OpenAI); vazio = desligada |
| `BO_EMBED_URL` | base do provedor — padrões: Ollama `http://127.0.0.1:11434`, OpenAI `https://api.openai.com/v1`; Azure: `https://<recurso>.openai.azure.com` |
| `BO_EMBED_MODEL` | modelo — padrões: `nomic-embed-text` (Ollama), `text-embedding-3-small` (OpenAI); no Azure, o **nome da implantação** |
| `BO_EMBED_KEY` | chave da API (OpenAI: `Authorization: Bearer`; Azure: cabeçalho `api-key`). Nunca vai para o log |
| `BO_EMBED_API_VERSION` | Azure: versão da API (padrão `2024-10-21`) |
| `BO_EMBED_QUERY_PREFIX` / `BO_EMBED_DOC_PREFIX` | texto antes da consulta / de cada memória. Padrão automático pelo modelo: `nomic-embed*` → `search_query: ` / `search_document: `; `*e5*` → `query: ` / `passage: `; outros, nada. `none` desliga o padrão. Mudar o prefixo dos documentos reindexa tudo |
| `BO_EMBED_MIN_SIMILARITY` | similaridade mínima (0 a 1, padrão `0.3`); suba se vierem "parecidas" demais — depende do modelo |

Exemplo local, com o Ollama:

```bash
ollama pull nomic-embed-text
BO_EMBED_PROVIDER=ollama go run ./cmd/bo-memory-server
```

O PostgreSQL do `docker compose` já é a imagem `pgvector/pgvector:pg16`. Em outro servidor,
instale o pacote do pgvector e garanta que o usuário do banco possa rodar
`CREATE EXTENSION vector` (ou crie a extensão antes, como superusuário).

**Privacidade:** o texto das memórias vai para o provedor configurado. Com dados sensíveis, prefira
um modelo local (Ollama) ou um provedor coberto pelo contrato da empresa (Azure OpenAI).

## Testes

`internal/semantic/fakeembed` é um provedor falso (fala os três formatos) com vetores por
"conceito" — os testes de `internal/semantic` e `internal/api` (e o fluxo `semantic` da web, com
o equivalente em `bo-memory-web/test/lib/fake-embed.js`) o usam; nada sai da máquina.
