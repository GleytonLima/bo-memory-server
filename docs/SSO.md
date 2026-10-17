# SSO — login pelo provedor de identidade da empresa

O servidor fala **OpenID Connect** (OIDC): Microsoft Entra ID, Google Workspace, Okta, Keycloak,
Auth0… Para o servidor, o Entra ID é só uma configuração.

## Como funciona

1. A web manda para `GET /auth/login`. O servidor gera `state`, `nonce` e PKCE e redireciona ao
   provedor.
2. O provedor volta em `GET /auth/callback`. O servidor troca o código, que é um cliente
   confidencial com segredo guardado só no servidor, e **valida o ID token**:
   - assinatura pelo JWKS;
   - emissor;
   - audiência igual ao client id;
   - validade;
   - nonce.
3. Usuário: pelo vínculo **emissor + `sub`**. No 1º login, um usuário criado antes pelo admin
   (mesmo e-mail) é vinculado e mantém o papel de admin. Se não houver nenhum, um usuário comum
   é criado. **E-mail**, nome e **employeeid** são atualizados a cada login.
4. O servidor emite uma **sessão** própria (`bos_…`, expira em `BO_SESSION_TTL`) e volta para a
   web com ela no fragmento da URL (`/#sso=…`), que não aparece em logs. A web guarda e usa como
   Bearer.

Tokens pessoais (`bom_…`) continuam valendo para scripts, CI e o desktop.
`BO_PERSONAL_TOKENS=false` faz o servidor aceitar só sessões SSO.

**Projetos por e-mail:** na administração da web, cada workspace pode ter **membros**: e-mails
(`ana@empresa.com`) ou o domínio inteiro (`*@empresa.com`).
- **Com lista:** só membros e admins leem, sincronizam e escrevem nele.
- **Sem lista:** o workspace é aberto a todos os usuários.

## Variáveis

| Variável | Exemplo | Padrão |
| --- | --- | --- |
| `BO_OIDC_ISSUER` | `https://login.microsoftonline.com/<tenant-id>/v2.0` | (sem SSO) |
| `BO_OIDC_CLIENT_ID` | id do aplicativo registrado | (sem SSO) |
| `BO_OIDC_CLIENT_SECRET` | segredo do aplicativo | — |
| `BO_PUBLIC_URL` | `https://memoria.empresa.com` | `http://localhost:8787` |
| `BO_OIDC_PROVIDER_NAME` | `Microsoft` (texto do botão "Entrar com …") | `SSO` |
| `BO_OIDC_SCOPES` | escopos pedidos | `openid profile email` |
| `BO_OIDC_EMAIL_CLAIM` | claims tentadas para o e-mail, em ordem | `email preferred_username upn` |
| `BO_OIDC_EMPLOYEE_ID_CLAIM` | claims tentadas para o employeeid | `employeeid employee_id employeeId` |
| `BO_SESSION_TTL` | validade da sessão | `12h` |
| `BO_PERSONAL_TOKENS` | `false` = só sessões SSO | `true` |

A **redirect URI** a registrar no provedor é `BO_PUBLIC_URL` + `/auth/callback`. Em produção,
use HTTPS.

## Microsoft Entra ID

1. **Registrar o aplicativo.** Entra admin center → *Identity* → *Applications* →
   *App registrations* → *New registration*.
   - Nome: "Memória do time".
   - Contas: *somente este diretório (single tenant)*.
   - Redirect URI: plataforma **Web**, `https://memoria.empresa.com/auth/callback`.
2. **Segredo.** *Certificates & secrets* → *New client secret*. O valor vai em
   `BO_OIDC_CLIENT_SECRET` e deve ser renovado antes de expirar.
3. **IDs.** Em *Overview*:
   - *Application (client) ID* → `BO_OIDC_CLIENT_ID`;
   - *Directory (tenant) ID* → `BO_OIDC_ISSUER=https://login.microsoftonline.com/<tenant-id>/v2.0`.

   Use o tenant específico, não `common`: o emissor do token precisa ser exatamente esse.
4. **E-mail.** O Entra manda `preferred_username` (normalmente o UPN, que costuma ser o e-mail)
   e `email` quando o usuário tem e-mail.
   - Para garantir o `email`: *Token configuration* → *Add optional claim* → *ID* → `email`.
5. **employeeid.** Não vem no ID token por padrão. Duas saídas:
   - **Política de mapeamento de claims** (*claims mapping policy*): mapeia
     `user.employeeid` para uma claim `employeeid`. Exige `"acceptMappedClaims": true` no
     *Manifest* do aplicativo. Pode ser criada pelo Microsoft Graph ou pelo PowerShell
     (`New-MgPolicyClaimMappingPolicy`) e associada ao *service principal* do aplicativo.
   - **Outro nome de claim** (ex.: um *directory extension* `extension_<appid>_employeeId`):
     informe esse nome em `BO_OIDC_EMPLOYEE_ID_CLAIM`.

   Sem isso o login funciona, só que com o employeeid vazio.
6. **Quem pode entrar.** Em *Enterprise applications* → o aplicativo → *Properties* →
   *Assignment required?* = **Yes**. Depois atribua usuários ou grupos em *Users and groups*.
   Sem isso, qualquer conta do tenant entra.
7. **Primeiro admin.**

   ```bash
   bo-memory-server admin create --name "Seu Nome" --email voce@empresa.com
   ```

   O e-mail precisa ser o mesmo do Entra. No 1º login pelo SSO, o usuário é vinculado e
   continua admin.

```bash
BO_OIDC_ISSUER=https://login.microsoftonline.com/<tenant-id>/v2.0
BO_OIDC_CLIENT_ID=<application-id>
BO_OIDC_CLIENT_SECRET=<segredo>
BO_OIDC_PROVIDER_NAME=Microsoft
BO_PUBLIC_URL=https://memoria.empresa.com
```

## Outros provedores

| Provedor | `BO_OIDC_ISSUER` | Observações |
| --- | --- | --- |
| Google Workspace | `https://accounts.google.com` | Cliente OAuth "Web application"; limite o domínio pelos membros (`*@empresa.com`). |
| Okta | `https://<org>.okta.com` (ou o authorization server) | App "Web", grant *Authorization Code*. |
| Keycloak | `https://<host>/realms/<realm>` | Client confidencial; mapper para `employeeid`, se usado. |

Requisitos de qualquer provedor:
- discovery em `/.well-known/openid-configuration`;
- ID token assinado (RS256 ou ES256);
- suporte a PKCE (S256).

## Diagnóstico

- **"login expirado ou inválido":** o login levou mais de 10 minutos ou foi reaberto noutra aba.
  Tente de novo.
- **"ID token inválido":** o emissor ou o client id não conferem (no Entra, tenant `common` ×
  específico), ou o relógio do servidor está errado.
- **"o provedor não informou … o e-mail":** ajuste `BO_OIDC_EMAIL_CLAIM` ou adicione a claim
  opcional `email`.
- Os detalhes de cada falha ficam no log do servidor (`SSO callback: …`).
