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
